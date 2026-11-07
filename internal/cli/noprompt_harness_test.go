package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/budget"
)

// noprompt_harness_test.go is the closed-stdin harness of the no-prompt policy
// (docs/no-prompt-policy/implementation-plan.md, T2b). Its constraint is the
// plan's first one — "no command waits on closed stdin" — and the shape of the
// check is the plan's: walk NewRootCmd, run every leaf with stdin a closed pipe
// and a short deadline, against a fake, and name every leaf that still asks.
//
// What a closed pipe proves, and what it does not. A pipe whose write end is
// closed is not a terminal and delivers EOF to the first read, so a prompt site
// that refuses when it cannot see a terminal returns at once — and still prints
// the question it will not read an answer to, which is what the second half of
// this harness fails on. A site that reads, gets EOF and asks again instead of
// giving up never returns at all, which is what the deadline fails on. Between
// the two, no leaf may wait and no leaf may ask: the question is the red signal
// today, and the deadline is the one that a prompt answering EOF by asking
// again trips.
//
// Every leaf must reach its own body. A leaf cobra stops at its own argument or
// flag check — `fugaro settings set` with the key and the value it requires,
// `fugaro restore` with the two it takes — has proved nothing about what the
// command does, and a bare run that ends in cobra's usage error is the one way
// this harness could have called a leaf quiet without ever asking it anything.
// So the run's wrapper marks the leaves whose RunE ran, and a leaf that did not
// get there fails until closedStdinFixtures gives it the arguments it needs (or
// closedStdinNotRun says why it cannot be run at all). The fixture is the
// arguments and, where the leaf is here to reach a question, the sentence it
// must still print: without that sentence a fixture whose arguments had merely
// stopped the leaf short would pass for a quiet one, which is a green answer for
// the wrong reason.
//
// The world every leaf runs in is the hermetic installation this package
// already builds — newBudgetFixture's temporary XDG, the local config naming
// project aurora, the RTDB fake and a file:// runs bucket — rebuilt for each
// leaf, so that a run which writes cannot decide what the next one finds. Three
// things are added to it. The outbound network is refused at once, by the same
// guard the ee CLI mains use (HTTPS_PROXY to a closed loopback port, which Go
// bypasses for loopback, so the fakes still answer); the ambient run's own state
// directory is cleared, or `fugaro verify` would run this repository's real
// build command instead of refusing; and no stream is a terminal and no agent
// marker is set, so what a leaf does about the closed stdin is the only thing
// the harness reads.
//
// The list this file exists to hold — closedStdinPrompts — is the allow-list the
// plan gives the lint of T1: a leaf that prints a question today is named there
// with the task of the plan that removes it, a leaf that prints one and is not
// named fails, and a named leaf that no longer prints one fails too, so the
// entry is deleted by the commit that removes the prompt rather than left to
// rot. It is the harness's one debt list, and reaching empty is the goal.
//
// closedStdinUnreached is the other half of the same ledger, and it is not a
// list of leaves that passed: it names the leaves whose question this world
// cannot drive them to — the write that asks only when it drops an approval, the
// verb whose confirmation sits behind a GCP read the fake does not serve — with
// the task that removes the ask and the source line that ask lives on. The line
// is read, so the entry cannot outlive the ask it is about: the task that
// deletes the confirmation in that file fails here until it deletes the entry
// too. Nothing in the list is claimed to be quiet, which is why the harness
// says so out loud rather than leaving a reader to assume a bare run settled it.

// closedStdinDeadline is how long a leaf may take with its stdin closed before
// the harness calls it a wait. It is accounts_add_login_test's and askWithin's
// own bound for the same question, against the package's ten-minute timeout,
// and it is generous on purpose: every leaf returns in milliseconds, and the
// one that does not is the failure this file is about.
const closedStdinDeadline = 10 * time.Second

// closedStdinQuestion matches the two shapes a run prints when it asks a person
// something: the ⚠ CONFIRM banner every typed step shows before it reads the
// project's name (confirmTyped, confirmTypedStrict, confirmBuild, confirmRemoval,
// the initflow steps, managed apply), and the prompt line itself. A value's
// prompt — readValue's "Type the billing account ID: " — is the same shape and
// is matched too, which is deliberate: it is only printed at a terminal, so a
// leaf that reaches it here has found a way to ask where nobody can answer.
var closedStdinQuestion = regexp.MustCompile(`⚠ CONFIRM|Type [^\n]*: $`)

// closedStdinFixture is one leaf's run: the arguments that put it on the path
// where it would ask somebody something, and the sentence the run must still
// print, which is what says the fixture really reached that path. Without the
// second the harness would call a leaf quiet that its own arguments had merely
// stopped before, which is a green answer for the wrong reason.
type closedStdinFixture struct {
	args    []string
	reaches string
	// seed is the state the leaf needs beyond the shared world — a kill switch
	// to resume, a bucket to name. Nil is the ordinary installation.
	seed func(t *testing.T, f *budgetFixture)
}

// closedStdinFixtures is the per-leaf fixture. A leaf named here runs with these
// arguments; a leaf that is not named runs bare, and then it must reach its own
// body with nothing added, which is the case for most of the tree: their whole
// run is the argument-free path.
//
// The block below is the leaves whose own arguments are what stands between a
// bare run and the command's work — cobra's usage error, not the command's
// answer. What each run is here to prove is the same thing in every case: the
// leaf's body, with a closed stdin, asks nothing and returns. `reaches` is
// pinned only where a question is what the fixture is for (closedStdinPrompts),
// because there the sentence is the proof the run got to the ask; elsewhere the
// proof is the body having run at all, which the harness checks for every leaf.
var closedStdinFixtures = map[string]closedStdinFixture{
	// The raise is what asks: the caps are unset in the fixture's database, so
	// setting one from nothing is a loosening.
	"fugaro budget set": {args: []string{"--global", "--per-run", "50"}, reaches: "global caps per-run: not set -> $50"},
	// The project-wide kill switch is the one that asks; a repository's is not.
	// Its sentence is the banner itself, because the kill is the whole of what
	// the run has said by the time it asks: T3, which removes the ask, changes
	// this sentence to whatever the run prints instead.
	"fugaro budget kill": {args: []string{"--all"}, reaches: "this halts every run"},
	// A resume asks only where there is a switch to move, so the fixture's
	// database is killed first: the ask is the subject of this run.
	"fugaro budget resume": {
		args:    []string{"--all"},
		reaches: "this lets project aurora start and continue runs again",
		seed: func(t *testing.T, f *budgetFixture) {
			f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "boss@example.com", "at": int64(1)})
		},
	},
	// The write asks before it rewrites the project's claude_accounts block.
	"fugaro accounts add": {args: []string{"team-a", "--repos", "acme/app"}, reaches: "claude_accounts"},
	// watch is the one leaf whose contract is not to return: it follows the
	// project until it is interrupted. --once is its own one snapshot, which is
	// the run this harness can hold to a deadline.
	"fugaro watch": {args: []string{"--once"}, reaches: "counted"},

	// The arguments the rest of the tree's leaves require. Each is the shallowest
	// set that reaches the command's body; what the body answers in this world is
	// the leaf's own business, and what the harness reads is that it answers with
	// its stdin closed and asks nothing.
	"fugaro accounts clear":       {args: []string{"team-a"}},
	"fugaro accounts rm":          {args: []string{"team-a"}},
	"fugaro allow-delete":         {args: []string{"someone@example.com"}},
	"fugaro artifacts get":        {args: []string{"run-1"}},
	"fugaro artifacts ls":         {args: []string{"run-1"}},
	"fugaro budget preview":       {args: []string{"--recipe", "r", "--runs", "1"}},
	"fugaro cancel":               {args: []string{"run-1"}},
	"fugaro creds set":            {args: []string{"NAME"}},
	"fugaro diagnose":             {args: []string{"run-1"}},
	"fugaro image add":            {args: []string{"base"}},
	"fugaro image gate":           {args: []string{"--record", "r", "--slug", "s", "--bucket", "file:///nonexistent"}},
	"fugaro image promote":        {args: []string{"base"}},
	"fugaro image record":         {args: []string{"--in", "r", "--digest-from", "a", "--base-digest-from", "b", "--image", "i", "--base", "ba", "--build-id", "x", "--slug", "s", "--bucket", "file:///nonexistent"}},
	"fugaro image rm":             {args: []string{"base"}},
	"fugaro logs":                 {args: []string{"run-1"}},
	"fugaro managed apply":        {args: []string{"root"}},
	"fugaro managed flag":         {args: []string{"proj-1234"}},
	"fugaro managed unflag":       {args: []string{"proj-1234"}},
	"fugaro models add":           {args: []string{"claude-x"}},
	"fugaro models rm":            {args: []string{"claude-x"}},
	"fugaro models search":        {args: []string{"x"}},
	"fugaro models use":           {args: []string{"a", "b", "c"}},
	"fugaro project repos add":    {args: []string{"acme/app"}},
	"fugaro project repos rm":     {args: []string{"acme/app"}},
	"fugaro recipes publish":      {args: []string{"slug"}},
	"fugaro recipes show":         {args: []string{"slug"}},
	"fugaro recipes validate":     {args: []string{"slug"}},
	"fugaro restore":              {args: []string{"proj-2", "root"}},
	"fugaro review show":          {args: []string{"run-1"}},
	"fugaro secrets rm":           {args: []string{"NAME"}},
	"fugaro secrets set":          {args: []string{"NAME"}},
	"fugaro settings get":         {args: []string{"a.b"}},
	"fugaro settings set":         {args: []string{"a.b", "1"}},
	"fugaro settings unset":       {args: []string{"a.b"}},
	"fugaro stats runs":           {args: []string{"--project", "proj-1234"}},
	"fugaro user add":             {args: []string{"someone@example.com"}},
	"fugaro user rm":              {args: []string{"someone@example.com"}},
	"fugaro verify":               {args: []string{"build"}},
	"fugaro workspace add":        {args: []string{"ws"}},
	"fugaro workspace member add": {args: []string{"ws", "acme/app"}},
	"fugaro workspace member rm":  {args: []string{"ws", "acme/app"}},
	"fugaro workspace rm":         {args: []string{"ws"}},
	"fugaro workspace show":       {args: []string{"ws"}},
}

// closedStdinNotRun is the listed exemption: a leaf the harness does not run,
// and why. It is empty, and that is the finding — every leaf of the tree
// reaches a verdict under a closed stdin, so none needs excusing. The list is
// here so that the first leaf that does need one is a decision written down
// beside its reason, and not an omission a reader has to notice.
var closedStdinNotRun = map[string]string{}

// closedStdinPrompts is the allow-list: the leaves that still print a question
// with a closed stdin, each with the task of the plan that removes it. Every
// entry is a promise to delete it in that task's commit, and the run's own check
// holds the promise — a leaf that stops asking fails until its entry is gone.
// The list reaching empty is what the policy's first constraint looks like from
// here.
var closedStdinPrompts = map[string]string{
	// A raise asks for the project's name before it loosens the budget; T3
	// removes the ask and leaves the owner-session fence in its place.
	"fugaro budget set": "T3",
	// The project-wide kill switch asks the same way, and T3 owns it too. A
	// repository's kill switch does not ask, which is why the fixture is --all.
	"fugaro budget kill": "T3",
	// The resume is the third of the raise forms T3 lists, and it asks with the
	// same typed name. It reaches the ask only where a switch is set, which its
	// fixture seeds.
	"fugaro budget resume": "T3",
	// The account write asks before it rewrites the project's claude_accounts
	// block and mints the account's secret version; T5 removes the ask.
	"fugaro accounts add": "T5",
}

// closedStdinUnreachedAsk is one ask this world cannot drive its leaf to: the
// file the ask is written in, the line that has to survive for the entry to be
// live, the task of the plan that removes the ask, and why the harness stops
// short of it.
type closedStdinUnreachedAsk struct {
	file   string
	marker string
	task   string
	why    string
}

// closedStdinUnreached names the leaves whose question is out of this harness's
// reach, and it is the honest half of the ledger: nothing here is proved quiet.
// Each entry is a promise of the same kind as closedStdinPrompts' — the task
// named removes the ask, and the commit that removes it has to delete this entry
// too, or the marker check below fails.
var closedStdinUnreached = map[string]closedStdinUnreachedAsk{
	// The write asks only when the list it writes drops an approval the bucket
	// already holds, which is not a state this world builds.
	"fugaro allow-delete": {
		file: "allow_delete.go", marker: "confirmTypedStrict(cmd, lc,",
		task: "T5", why: "asks only where the write drops an approval it read in the bucket",
	},
	// The restore refuses a session with no terminal before its banner, in a
	// sentence of its own, so the banner is never printed here.
	"fugaro restore": {
		file: "restore.go", marker: "func (rr *restoreRun) confirm(cmd *cobra.Command) error {",
		task: "T6", why: "refuses a session with no terminal before the banner it would print",
	},
	// The flag reads the project's number from the cloud before its banner, and
	// the fake serves no Resource Manager.
	"fugaro managed flag": {
		file: "managed_flag.go", marker: "confirmTypedGCPProject(cmd, lc.GCPProject)",
		task: "T6", why: "prints its banner after a project-number read the fake does not serve",
	},
	"fugaro managed unflag": {
		file: "managed_unflag.go", marker: "confirmTypedGCPProject(cmd, lc.GCPProject)",
		task: "T6", why: "prints its banner after the managed marker read, which this world refuses",
	},
	// managed apply asks from inside an apply: the question carries a saved
	// plan's hash and counts, so it takes a planned root this world has none of.
	"fugaro managed apply": {
		file: "managed_apply.go", marker: "func managedAsk(cmd *cobra.Command, lc *localcfg.Config)",
		task: "T9", why: "asks inside an apply, which needs a saved plan this world does not build",
	},
	// A settings write asks from the layer or checkout it edits, and the typing
	// rules that decide which of the two confirmations applies are the write's
	// own; this world has no checkout to edit.
	"fugaro settings set": {
		file: "settings_write.go", marker: "confirmTypedStrict(cmd, lc, e.TypedWhy, e.What)",
		task: "T5", why: "asks from the layer or checkout it edits, and this world has neither",
	},
	"fugaro settings unset": {
		file: "settings_write.go", marker: "func confirmSettingsWrite(",
		task: "T5", why: "asks from the layer or checkout it edits, and this world has neither",
	},
	// A build's confirmation stands behind the rig that builds a real image:
	// the registry, the build record and the Cloud Build API it submits to.
	"fugaro image build": {
		file: "image.go", marker: "func confirmBuild(cmd *cobra.Command, project, gcpProject, what string) error {",
		task: "T4", why: "confirms at the submit, which needs the build rig's registry and Cloud Build fake",
	},
	"fugaro image refresh": {
		file: "image_refresh_steps.go", marker: "r.confirm(",
		task: "T4", why: "asks from the refresh's own plan, which needs the jobs this world does not fake",
	},
	// The published policy's own two writes ask before they replace rules
	// nothing can read back after them; both stand behind a deployed-rules read.
	"fugaro project policy publish": {
		file: "project_cmd.go", marker: "confirmRemoval(cmd, lc, o.yes,",
		task: "T7", why: "asks after a deployed-rules read, which this world refuses",
	},
	// An un-enrollment asks once runs are in flight, or where it drops an
	// approval the rules already carry.
	"fugaro project sync": {
		file: "project_enroll.go", marker: "confirmRemovalStrict(cmd, lc,",
		task: "T6", why: "asks where the sync drops an approval the deployed rules hold",
	},
	// The rules deployment asks as the owner, through the database client this
	// world keeps out of the dev-build path.
	"fugaro upgrade": {
		file: "upgrade.go", marker: "confirmRemoval(u.cmd, lc, u.o.yes,",
		task: "T5", why: "asks when it deploys the security rules, which a development build skips",
	},
	// update and setup ask from their stages: the workspace and layer steps, the
	// policy steps, and the wizard's own confirmations.
	"fugaro update": {
		file: "update_workspace.go", marker: "confirmTypedStrict(cmd, p.lc, why, what)",
		task: "T15", why: "asks from its stages, which need an installation this world does not build",
	},
	"fugaro setup": {
		file: "init.go", marker: "func (r *initRun) ask(what string) (bool, error) {",
		task: "T7", why: "asks from the wizard's steps, which need the cloud fixture to reach",
	},
}

// TestEveryCommandRunsWithClosedStdin runs every leaf of the released tree with
// its stdin closed and holds it to the policy: it returns, its own body runs, it
// reaches the work its fixture is for, and it asks nothing.
func TestEveryCommandRunsWithClosedStdin(t *testing.T) {
	leaves := closedStdinLeaves(t)
	paths := closedStdinPaths(leaves)
	ran := 0
	for _, leaf := range leaves {
		path := leaf.CommandPath()
		if why, exempt := closedStdinNotRun[path]; exempt {
			if why == "" {
				t.Errorf("%s is exempt with no reason: an exemption nobody can read is an omission", path)
			}
			continue
		}
		ran++
		t.Run(path, func(t *testing.T) {
			// One world per leaf: a run that writes must not decide what the
			// next leaf finds, whatever order the tree walks in.
			f := closedStdinWorld(t)
			// What the leaf needs beyond the shared world, before it runs: a
			// switch for the resume to move, and nothing at all for most leaves.
			if fixture := closedStdinFixtures[path]; fixture.seed != nil {
				fixture.seed(t, f)
			}
			out, returned, entered, err := closedStdinRun(t, path)
			if !returned {
				t.Fatalf("%s did not return within %s with its stdin closed: a read that waits for a person is a wait nobody can end (it printed %q)", path, closedStdinDeadline, out)
			}
			if !entered {
				t.Fatalf("%s never reached its own body with its stdin closed (%v), so this run says nothing about what the command does: give it the arguments it needs in closedStdinFixtures, or list it in closedStdinNotRun with the reason it cannot be run", path, err)
			}
			if fixture, ok := closedStdinFixtures[path]; ok && fixture.reaches != "" && !strings.Contains(out, fixture.reaches) {
				t.Fatalf("the fixture for %s no longer reaches %q, so it refuses before the work it is here to reach: the run printed %q", path, fixture.reaches, out)
			}
			closedStdinAsked(t, path, out)
		})
	}
	if ran == 0 {
		t.Fatal("the walk found no leaf to run: the harness is reading nothing")
	}
	// A name that must be a leaf whatever else moves, so that a walk which
	// stopped finding the tree fails here rather than passing over nothing.
	if !slices.Contains(paths, "fugaro version") {
		t.Fatal("the walk does not find `fugaro version`, which every build holds: the tree it walks is not the released one")
	}
	for _, named := range [][]string{
		closedStdinNamed(closedStdinFixtures),
		closedStdinNamed(closedStdinNotRun),
		closedStdinNamed(closedStdinPrompts),
		closedStdinNamed(closedStdinUnreached),
	} {
		for _, path := range named {
			if !slices.Contains(paths, path) {
				t.Errorf("%s is named a leaf and is not one: a fixture, an exemption or a prompt entry that outlives its command is a rule about nothing", path)
			}
		}
	}
	for path := range closedStdinPrompts {
		if fixture, ok := closedStdinFixtures[path]; !ok || fixture.reaches == "" {
			t.Errorf("%s is listed as asking without a fixture that reaches the ask: an entry the harness cannot show is a claim about a run nobody made", path)
		}
	}
	closedStdinUnreachedIsLive(t)
}

// closedStdinUnreachedIsLive holds the unreached ledger to the source it is
// about: the ask each entry names has to still be written where the entry says,
// so the commit that removes the confirmation fails here until it removes the
// entry beside it.
func closedStdinUnreachedIsLive(t *testing.T) {
	t.Helper()
	for path, ask := range closedStdinUnreached {
		source, err := os.ReadFile(filepath.Join(closedStdinSourceDir, ask.file))
		if err != nil {
			t.Errorf("%s is listed with an ask in %s, which cannot be read here: %v", path, ask.file, err)
			continue
		}
		if ask.task == "" || ask.why == "" {
			t.Errorf("%s is listed as unreachable with no task or no reason: the entry is the whole of what a reader has to go on", path)
		}
		if !strings.Contains(string(source), ask.marker) {
			t.Errorf("%s is listed as asking somewhere this harness cannot reach, and %s no longer holds %q: the ask it names is gone, so the entry goes with it", path, ask.file, ask.marker)
		}
	}
}

// closedStdinAsked holds one leaf's output to the policy's other half: a
// question printed with a closed stdin is a prompt nobody can answer, and it is
// either named in closedStdinPrompts or it is a failure. The reverse is checked
// here too, so the list cannot keep an entry whose prompt the task that owns it
// has removed.
func closedStdinAsked(t *testing.T, path, out string) {
	t.Helper()
	question := closedStdinQuestion.FindString(out)
	if question == "" {
		if owner, listed := closedStdinPrompts[path]; listed {
			t.Errorf("%s no longer prints a question with its stdin closed, so its closedStdinPrompts entry (%s) is stale: delete it in this commit", path, owner)
		}
		return
	}
	if _, listed := closedStdinPrompts[path]; listed {
		return
	}
	t.Errorf("%s printed a question with its stdin closed (%q): a closed stdin is nobody to answer, so the run asks a person who is not there\n%s", path, question, out)
}

// closedStdinWorld is the state every leaf is run in: one hermetic
// installation, no outbound network, no terminal and no agent marker.
func closedStdinWorld(t *testing.T) *budgetFixture {
	t.Helper()
	// A leaf must not be able to write into the checkout the tests run from
	// (fugaro.yaml, .claude/, a layer file): the harness runs in a directory of
	// its own, which is also the honest "no checkout here" case.
	t.Chdir(t.TempDir())
	f := newBudgetFixture(t, "")
	// The same guard the ee CLI mains carry: an outbound request must not leave
	// this machine. Go bypasses the proxy for loopback, so the fixtures' own
	// servers still answer.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	// The update check is a GitHub request on the command's own stderr: the
	// harness's streams are buffers, so it never starts, and the switch says so
	// rather than leaving it to a stream's type.
	t.Setenv("FUGARO_NO_UPDATE_CHECK", "1")
	// The run this test runs inside names its own state directory, and `fugaro
	// verify` reads it: with it set, the leaf would run this repository's real
	// build command and take minutes, and the harness would be measuring the
	// machine rather than the command.
	t.Setenv("FUGARO_STATE_DIR", "")
	// Which machine this is must not decide what a run does: a coding agent's
	// session refuses writes the closed-stdin case would otherwise reach, and a
	// real terminal is what this harness is the opposite of.
	clearAgentMarkers(t)
	terminal(t, false)
	return f
}

// closedStdinRun runs one leaf with its stdin a closed pipe and reports what it
// printed, whether it returned before the deadline, whether its own body ran at
// all, and what it returned. The output is a syncBuf's because a run that timed
// out is still writing to it.
func closedStdinRun(t *testing.T, path string) (string, bool, bool, error) {
	t.Helper()
	// The leaf's own words, without the program name: cobra is handed the
	// arguments after `fugaro`, and the path's first word is the binary.
	argv := strings.Fields(path)[1:]
	if fixture, ok := closedStdinFixtures[path]; ok {
		argv = append(argv, fixture.args...)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The write end is closed before the run, so the pipe is at EOF: this is
	// the closed stdin the policy names, and it is an *os.File, which is what
	// makes stdinIsTerminal answer false for the unpatched reason too.
	w.Close()
	defer r.Close()
	out := &syncBuf{}
	// The tree is this run's own, so the wrapper that marks the body is too: a
	// tree shared with the next leaf would carry this one's flags into it.
	root := NewRootCmd()
	var entered atomic.Bool
	leaf := closedStdinLeaf(root, path)
	if leaf == nil {
		t.Fatalf("%s is not in the tree NewRootCmd builds", path)
	}
	if run := leaf.RunE; run != nil {
		leaf.RunE = func(c *cobra.Command, args []string) error {
			entered.Store(true)
			return run(c, args)
		}
	} else if plain := leaf.Run; plain != nil {
		leaf.Run = func(c *cobra.Command, args []string) {
			entered.Store(true)
			plain(c, args)
		}
	} else {
		t.Fatalf("%s has neither Run nor RunE", path)
	}
	root.SetIn(r)
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(NormalizeArgs(argv))
	done := make(chan struct{})
	var execErr error
	go func() {
		defer close(done)
		execErr = root.Execute()
	}()
	select {
	case <-done:
		return out.String(), true, entered.Load(), execErr
	case <-time.After(closedStdinDeadline):
		// entered is not read here: the run is still going, and the leaf that
		// did not return is the failure.
		return out.String(), false, false, nil
	}
}

// closedStdinLeaf is the leaf of root whose command path is path, or nil.
func closedStdinLeaf(root *cobra.Command, path string) *cobra.Command {
	for _, leaf := range closedStdinLeavesOf(root) {
		if leaf.CommandPath() == path {
			return leaf
		}
	}
	return nil
}

// closedStdinLeaves is every command of the released tree with no subcommand:
// the commands a run can end on. Cobra's own `help` and `completion` are not in
// the tree NewRootCmd builds — cobra adds them when a tree is executed — so a
// leaf here is always a command fugaro ships.
func closedStdinLeaves(t *testing.T) []*cobra.Command {
	t.Helper()
	return closedStdinLeavesOf(NewRootCmd())
}

func closedStdinLeavesOf(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 {
			leaves = append(leaves, c)
			return
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return leaves
}

// closedStdinPaths is the leaves' own paths, for the checks that a name in one
// of the lists is a command the tree still holds.
func closedStdinPaths(leaves []*cobra.Command) []string {
	paths := make([]string, 0, len(leaves))
	for _, leaf := range leaves {
		paths = append(paths, leaf.CommandPath())
	}
	return paths
}

// closedStdinNamed is one list's keys, as a slice.
func closedStdinNamed[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// closedStdinSourceDir is where the unreached ledger's files are read from: the
// package itself, which is the working directory `go test` runs this in.
var closedStdinSourceDir = "."

// TestClosedStdinHarnessSeesAQuestion holds the harness's own reading against
// runs whose answer is known, so that a green TestEveryCommandRunsWithClosedStdin
// means the leaves ask nothing rather than that a question went unnoticed. Both
// shapes are here — the banner and the prompt line — beside the refusals that
// must not be read as either.
func TestClosedStdinHarnessSeesAQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "the typed confirmation of a budget raise",
			out:  "config/caps/global: global caps per-run: not set -> $50\n⚠ CONFIRM (project aurora): this loosens the project's budget: runs may spend more than before\n",
			want: true,
		},
		{
			name: "the prompt line on its own",
			out:  "Type aurora to confirm: ",
			want: true,
		},
		{
			name: "a value's prompt",
			out:  "Type the billing account ID: ",
			want: true,
		},
		{
			name: "the refusal that replaces the prompt",
			out:  "the typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe\n",
			want: false,
		},
		{
			name: "a note that merely mentions a prompt",
			out:  "the value is empty; pipe it in on stdin or paste it at the prompt\n",
			want: false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := closedStdinQuestion.MatchString(c.out); got != c.want {
				t.Errorf("the harness reads a question in %q as %v, want %v", c.out, got, c.want)
			}
		})
	}
	// The ledger's files are read from this package's own directory, which the
	// world's t.Chdir moves: this is where that assumption is stated, and the
	// check above it reads before any subtest has moved anything.
	if _, err := os.Stat(filepath.Join(closedStdinSourceDir, "managed_flag.go")); err != nil {
		t.Errorf("the unreached ledger reads this package's files from %q, which does not hold them: %v", closedStdinSourceDir, err)
	}
}
