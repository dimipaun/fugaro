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
// Every leaf must reach its own body, and every leaf must be classified. A leaf
// that stops at its own argument or flag check — cobra's usage error for a
// command whose Args validator is declared on it, the command's own refusal for
// one that reads `len(args)` or a flag inside its body — has proved nothing
// about what the command does, and a bare run that ends there is the one way
// this harness could have called a leaf quiet without ever asking it anything.
// Two checks answer it. The run's wrapper marks the leaves whose RunE ran, and
// a leaf that did not get there fails; and every leaf of the tree has to be in
// closedStdinFixtures or in closedStdinNoFixture, so the leaves the body check
// cannot tell apart — a command that entered its body and stopped at the
// argument check written there — are a decision someone wrote down rather than
// a count that went up.
//
// The fixture is the arguments and, where the leaf is here to reach a question,
// the sentence it must still print: without that sentence a fixture whose
// arguments had merely stopped the leaf short would pass for a quiet one, which
// is a green answer for the wrong reason.
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

// closedStdinQuestion matches what a run prints when it asks a person
// something. There are two readings in it, and between them they cover every
// prompt the tree writes.
//
// The ⚠ CONFIRM banner is the first: every typed step prints it before it reads
// the project's name (confirmTyped, confirmTypedStrict, confirmBuild,
// confirmRemoval, the initflow steps, managed apply). It is matched anywhere,
// because it is a line of its own and the question comes after it.
//
// The second reading is the prompt line itself, and what every prompt in this
// tree has in common is that its line ends in its own colon and a space. That
// is the whole of it — the typed confirmations ("Type %s to confirm: "), the
// setup wizard's answers (promptChoice's "%s [%s]: " and its " (or %s: %s)"
// form, and the plain "%s: " when there is no default: the GCP project, the
// billing account, the GitHub App ID, the region, the onboarding question),
// initsecrets' "Paste the private key … then press Enter: ", pair's lower-case
// "type that code: ", the y/N questions ("Write this to %s? [y/N]: "), and
// readValue's value prompt ("Type the billing account ID: ") — and matching
// the shape rather than the wording is what keeps a prompt this file has never
// heard of from passing green. `(?m)` is what makes the `$` the end of a line
// rather than the end of the run: a run goes on after it asks — with the
// refusal an ended stdin earns, with the error cobra prints — and without the
// flag the only prompt caught was the last thing a run printed.
//
// The y/N question is read a second time, unanchored, because the
// print-then-refuse ordering it guards against puts the refusal on the
// prompt's own line. The five prints of the bracketed form are its only
// writers, so it is a question wherever it appears.
//
// Reading a line that ends in ": " as a question is deliberately generous: no
// leaf of the clean tree prints one (TestEveryCommandRunsWithClosedStdin is
// what says so), and a false positive is a failure that names the run and
// prints its output, which is a sentence away from being read.
var closedStdinQuestion = regexp.MustCompile(`(?m)⚠ CONFIRM|: $|\[y/N\]:`)

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
// arguments; a leaf that is not named runs bare and is named in
// closedStdinNoFixture, which is the other half of the same classification:
// every leaf of the tree is in one of the two, and the run fails on the leaf
// that is in neither.
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
	// The settings write asks with the typed name of the document it edits, and
	// what it edits here is the world's own project config. The key is a real
	// one: an unknown key stops at the registry's nearest-name refusal, and a
	// fixture that stopped there would call the leaf quiet without the write
	// having begun. The unset needs a value to remove, which its seed sets.
	"fugaro settings set":   {args: []string{"base_branch", "main"}, reaches: "⚠ CONFIRM (project aurora): write base_branch"},
	"fugaro settings unset": {args: []string{"base_branch"}, reaches: "⚠ CONFIRM (project aurora): remove base_branch", seed: closedStdinSetBaseBranch},
	// watch is the one leaf whose contract is not to return: it follows the
	// project until it is interrupted. --once is its own one snapshot, which is
	// the run this harness can hold to a deadline.
	"fugaro watch": {args: []string{"--once"}, reaches: "counted"},

	// The arguments the rest of the tree's leaves require. Each is the shallowest
	// set that reaches the command's body; what the body answers in this world is
	// the leaf's own business, and what the harness reads is that it answers with
	// its stdin closed and asks nothing.
	"fugaro accounts clear":       {args: []string{"team-a"}},
	"fugaro workspace login":      {args: []string{"acme"}},
	"fugaro accounts rm":          {args: []string{"team-a"}},
	"fugaro allow-delete":         {args: []string{"someone@example.com"}},
	"fugaro artifacts get":        {args: []string{"run-1"}},
	"fugaro artifacts ls":         {args: []string{"run-1"}},
	"fugaro budget preview":       {args: []string{"--recipe", "r", "--runs", "1"}},
	"fugaro budget history":       {args: []string{"--sweep"}},
	"fugaro cancel":               {args: []string{"run-1"}},
	"fugaro creds set":            {args: []string{"NAME"}},
	"fugaro diagnose":             {args: []string{"run-1"}},
	"fugaro exec":                 {args: []string{"--bucket", "file:///nonexistent"}},
	"fugaro image add":            {args: []string{"base"}},
	"fugaro image gate":           {args: []string{"--record", "r", "--slug", "s", "--bucket", "file:///nonexistent"}},
	"fugaro image git-credential": {args: []string{"--out", "git-cred"}},
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
	"fugaro pair revoke":          {args: []string{"--uid", "uid-1"}},
	"fugaro project repos add":    {args: []string{"acme/app"}},
	"fugaro project repos rm":     {args: []string{"acme/app"}},
	"fugaro recipes publish":      {args: []string{"slug"}},
	"fugaro recipes show":         {args: []string{"slug"}},
	"fugaro recipes validate":     {args: []string{"slug"}},
	"fugaro restore":              {args: []string{"proj-2", "root"}},
	"fugaro review show":          {args: []string{"run-1"}},
	"fugaro run":                  {args: []string{"do something"}},
	"fugaro secrets rm":           {args: []string{"NAME"}},
	"fugaro secrets set":          {args: []string{"NAME"}},
	"fugaro settings get":         {args: []string{"a.b"}},
	"fugaro stats runs":           {args: []string{"--project", "proj-1234"}},
	"fugaro user add":             {args: []string{"someone@example.com"}},
	"fugaro user rm":              {args: []string{"someone@example.com"}},
	"fugaro verify":               {args: []string{"build"}},
	"fugaro workspace add":        {args: []string{"ws"}},
	"fugaro workspace cap set":    {args: []string{"--workspace", "ws", "--monthly", "50"}},
	"fugaro workspace member add": {args: []string{"ws", "acme/app"}},
	"fugaro workspace member rm":  {args: []string{"ws", "acme/app"}},
	"fugaro workspace rm":         {args: []string{"ws"}},
	"fugaro workspace show":       {args: []string{"ws"}},
}

// closedStdinNoFixture is the other half of the classification, and the reason
// every leaf of the tree has to be in one of the two: a leaf with no fixture
// runs bare, and a bare run of a command that takes arguments stops at its own
// argument check — cobra's usage error for the ones whose Args validator is
// declared on the command, the command's own refusal for the ones that read
// `len(args)` or a flag in its body — which says nothing about what the command
// does with them. Left unlisted, that is a green run for a leaf nobody drove.
//
// So a leaf is fixtured or it is named here with what its bare run is: the
// leaves whose whole work is the argument-free path, and the few whose own
// checks stop the bare run before that work and which this world has no
// arguments for. Nothing here is claimed to be quiet either — the question
// check and the deadline hold every leaf in this list exactly as they hold a
// fixtured one, which is what makes the list safe to keep.
//
// The commands that carry subcommands and a body of their own are here too,
// and for the same reason there is no fixture for them: a parent's own body is
// what runs when no subcommand matches, `fugaro settings` bare is the whole of
// that command's read, and the four that print their usage have no argument
// that would do more. `fugaro review` and `fugaro pair` refuse from inside
// their bodies, in sentences of their own, which the run's own error carries.
var closedStdinNoFixture = map[string]string{
	"fugaro accounts":               "is a name for its four subcommands: a bare run prints its own usage and returns",
	"fugaro accounts ls":            "selects no project and names the two ways to (--repo, an origin remote)",
	"fugaro backends":               "is a name for its one subcommand: a bare run prints its own usage and returns",
	"fugaro backends ls":            "lists every backend a project may name, which needs no project",
	"fugaro backlog add":            "stops at its own D27 field checks, which this world has no values for",
	"fugaro backlog index":          "reads docs/backlog, which this world has no checkout to hold",
	"fugaro backlog list":           "reads docs/backlog, which this world has no checkout to hold",
	"fugaro budget prices":          "prints the built-in price list with no project selected",
	"fugaro budget show":            "prints the no-project-config refusal",
	"fugaro config example":         "prints the example fugaro.yaml",
	"fugaro config init":            "refuses outside a git checkout, which is what the world is",
	"fugaro config layer":           "refuses with no checkout and no selected project config",
	"fugaro config list":            "prints that this machine holds no installation",
	"fugaro config publish":         "refuses with no fugaro.yaml to publish",
	"fugaro config show":            "refuses outside a git checkout, which is what the world is",
	"fugaro coordinator":            "refuses: the edition is not in this build",
	"fugaro credentials serve":      "refuses without FUGARO_PROJECT in its own environment",
	"fugaro creds":                  "is a name for set and ls: a bare run prints its own usage and returns",
	"fugaro creds ls":               "prints the no-project-config refusal",
	"fugaro doctor":                 "runs its whole check and reports what this world lacks",
	"fugaro graph":                  "refuses: the edition is not in this build",
	"fugaro image build":            "refuses before the build: a billable build's confirmation needs a real terminal",
	"fugaro image check":            "prints the no-project-config refusal",
	"fugaro image ls":               "prints the no-project-config refusal",
	"fugaro image refresh":          "refuses before its steps: each asks for the project's name at a real terminal",
	"fugaro image render":           "refuses outside a git checkout, which is what the world is",
	"fugaro image selftest":         "reads its spec from the closed stdin and returns on the EOF",
	"fugaro image status":           "prints the no-project-config refusal",
	"fugaro managed status":         "prints the no-project-config refusal",
	"fugaro models ls":              "prints the built-in list with no project selected",
	"fugaro pair":                   "refuses from inside its body: the world's project has no budget.firebase_api_key for the phone to exchange the token with",
	"fugaro pair ls":                "prints the no-project-config refusal",
	"fugaro plan graph":             "refuses with no graph artifact under docs/plans",
	"fugaro plan status":            "reads the repository's plan and stream files and reports what is missing",
	"fugaro project policy publish": "refuses before its ask: the deployed rules here carry no policy marker",
	"fugaro project policy show":    "prints that this installation has published no policy",
	"fugaro project repos ls":       "prints the no-project-config refusal",
	"fugaro project show":           "prints the no-project-config refusal",
	"fugaro project sync":           "refuses before its write: the deployed rules this world serves carry no policy marker",
	"fugaro reaper":                 "prints the no-project-config refusal",
	"fugaro recipes ls":             "prints the no-project-config refusal",
	"fugaro report":                 "refuses: the edition is not in this build",
	"fugaro review":                 "refuses from inside its body: --pr is required, and a bare run names it rather than launching a review",
	"fugaro runs digest":            "prints the no-project-config refusal",
	"fugaro runs ls":                "prints the no-project-config refusal",
	"fugaro runs stats":             "prints the no-project-config refusal",
	"fugaro runs summary":           "prints the no-project-config refusal",
	"fugaro seats":                  "counts this machine's two member lists and the last 90 days of run requesters, which this world's empty bucket gives as nobody",
	"fugaro secrets":                "is a name for set, ls and rm: a bare run prints its own usage and returns",
	"fugaro secrets ls":             "prints the no-project-config refusal",
	"fugaro settings":               "prints the registry's every row with no project selected, which is the whole of what a bare run does",
	"fugaro setup":                  "refuses on this world's runs bucket name before the wizard's first step",
	"fugaro update":                 "refuses on this world's runs bucket name before its first stage",
	"fugaro update-skills":          "reports that a development build has no release to pin the plugin to",
	"fugaro upgrade":                "skips every step: a development build has no release to pin or install",
	"fugaro user ls":                "prints the no-project-config refusal",
	"fugaro validate":               "refuses with no fugaro.yaml in the world",
	"fugaro version":                "prints the version",
	"fugaro workspace ls":           "prints that no config on this machine names a workspace",
}

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
	// Both halves of the generic settings write ask with the typed name of the
	// document they edit; T5 removes the asks. They are here rather than in the
	// unreached ledger because the world's own project config is the document
	// they edit, so a fixture that names a real setting reaches the banner.
	"fugaro settings set":   "T5",
	"fugaro settings unset": "T5",
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
	// already holds. Reaching that means a managed installation — the runs
	// bucket under its default name, the endpoints the read needs, and an
	// allow-list object — and this world is a project config with a file://
	// bucket, so the run refuses before it reads anything.
	"fugaro allow-delete": {
		file: "allow_delete.go", marker: "confirmTypedStrict(cmd, lc,",
		task: "T5", why: "asks only where the write drops a read approval, which needs a managed installation this world is not",
	},
	// managed apply asks from inside an apply: the question carries a saved
	// plan's hash and counts, so it takes a planned root this world has none of.
	"fugaro managed apply": {
		file: "managed_apply.go", marker: "func managedAsk(cmd *cobra.Command, lc *localcfg.Config)",
		task: "T9", why: "asks inside an apply, which needs a saved plan this world does not build",
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
				t.Fatalf("%s never reached its own body with its stdin closed (%v), so this run says nothing about what the command does: give it the arguments it needs in closedStdinFixtures", path, err)
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
	// Every leaf is fixtured or exempted, and every exemption is a reason. A
	// leaf in neither is the one way this harness could pass a command it never
	// drove, so it fails here naming the leaf rather than joining the count.
	for _, leaf := range leaves {
		path := leaf.CommandPath()
		_, fixtured := closedStdinFixtures[path]
		why, exempt := closedStdinNoFixture[path]
		if closedStdinRetiredLeaf(leaf) {
			exempt, why = true, closedStdinRetiredWhy
		}
		switch {
		case fixtured && exempt:
			t.Errorf("%s is fixtured and exempt at once: the fixture is what a bare run cannot do, so one of the two is stale", path)
		case !fixtured && !exempt:
			t.Errorf("%s is neither fixtured nor exempt: its bare run stops at whatever arguments it takes, so this run would say nothing about the command. Give it the arguments it needs in closedStdinFixtures, or say in closedStdinNoFixture what its bare run is", path)
		case exempt && why == "":
			t.Errorf("%s is exempt with no reason: an exemption nobody can read is an omission", path)
		}
	}
	// A fixture with no arguments is a bare run wearing a fixture's name, and
	// the classification above would then hold a leaf that is in fact unfixtured.
	for path, fixture := range closedStdinFixtures {
		if len(fixture.args) == 0 {
			t.Errorf("%s has a fixture with no arguments: that is a bare run, so either give it what it needs or move it to closedStdinNoFixture", path)
		}
	}
	for _, named := range [][]string{
		closedStdinNamed(closedStdinFixtures),
		closedStdinNamed(closedStdinNoFixture),
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

// closedStdinRetiredLeaf reports the tree's one tombstone (retired.go). It is a
// leaf like any other — the harness runs it and holds it to the deadline and to
// the question check — but it is exempt from the classification by this rule
// rather than by its path, because its path is the one string the live-tree
// census (docs_live_init_census_test.go, W0.2) allows in the files that retire
// the command and nowhere else, and this is not one of them. Both the sentence
// it prints and the way it names itself say what it is.
func closedStdinRetiredLeaf(leaf *cobra.Command) bool {
	return leaf.Hidden && strings.HasPrefix(leaf.Short, "Retired:")
}

// closedStdinRetiredWhy is that exemption's reason, beside the same list's rule.
const closedStdinRetiredWhy = "is the tombstone: it prints the two commands that replaced it and exits non-zero"

// closedStdinSetBaseBranch gives the world's project config a value for the
// setting the unset's fixture removes: an unset of a key no document sets has
// nothing to confirm and says so, which would be the wrong reason for a green
// run.
func closedStdinSetBaseBranch(t *testing.T, f *budgetFixture) {
	t.Helper()
	f.appendConfig(t, "base_branch: main\n")
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

// closedStdinLeaves is every command of the released tree a run can end on:
// the commands with no subcommand, and the commands that carry a subcommand
// tree and a body of their own. The second kind is here because the policy is
// about every command and not only the ones nothing hangs from: cobra runs a
// parent's body when no subcommand matches the arguments, so `fugaro settings`
// bare prints every setting and `fugaro review` bare refuses its missing --pr
// from inside that body — both of them a closed stdin away from the same
// question the leaves are held to. A parent with subcommands and no body of its
// own is a name for its children and runs nothing, so it is not one of these.
//
// Cobra's own `help` and `completion` are not in the tree NewRootCmd builds —
// cobra adds them when a tree is executed — so a leaf here is always a command
// fugaro ships.
func closedStdinLeaves(t *testing.T) []*cobra.Command {
	t.Helper()
	return closedStdinLeavesOf(NewRootCmd())
}

func closedStdinLeavesOf(root *cobra.Command) []*cobra.Command {
	var leaves []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if len(c.Commands()) == 0 || c.Run != nil || c.RunE != nil {
			leaves = append(leaves, c)
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
			// The one the anchoring is for: a prompt is a line, and the run
			// goes on after it — with the refusal an ended stdin earns, with a
			// cobra error, with anything. A run was caught when the prompt was
			// the last thing it printed and missed when one line followed it,
			// so this is the case that has to stay green.
			name: "a prompt with the run's refusal below it",
			out:  "Type aurora to confirm: \nthe typed confirmation of this step needs a real terminal: run it in your own terminal window, not through a coding agent or a pipe\n",
			want: true,
		},
		{
			// No banner anywhere in it: the y/N family prints the question and
			// nothing else, so this is the shape that matches on its own.
			name: "the y/N prompt, which prints no banner",
			out:  "Write this to /tmp/x/fugaro.yaml? [y/N]: ",
			want: true,
		},
		{
			// The same ordering hazard for the y/N family: the prompt is on the
			// line the run continues on.
			name: "a y/N prompt the run continues past",
			out:  "Write this to /tmp/x/fugaro.yaml? [y/N]: the confirmation of this write needs a real terminal\n",
			want: true,
		},
		{
			// The wizard's own question, which says neither "Type" nor "[y/N]":
			// promptChoice prints the label with the default it would take in
			// brackets, and a leaf that reached it and read the ended stdin
			// would answer it with the default rather than with a person.
			name: "the setup wizard's answer, with its default in brackets",
			out:  "GCP project ID [aurora]: ",
			want: true,
		},
		{
			// The same line with the second answer promptChoice offers: the
			// brackets are not at the end here, so this is the case that says
			// the reading is the line's ending and not the bracket.
			name: "the wizard's question with the alternative answer after it",
			out:  "GCP project ID [aurora] (or 123456: create that project instead): ",
			want: true,
		},
		{
			// promptChoice with no default prints no bracket at all: the GitHub
			// App ID is asked this way on a repository that has no stored one.
			name: "the wizard's question with no default",
			out:  "GitHub App ID for acme/app (a number, not a secret; on the App's settings page): ",
			want: true,
		},
		{
			// The hidden value prompt, which is a sentence rather than a label.
			name: "the hidden value prompt",
			out:  "Paste the private key for fugaro-app (input hidden), from its -----BEGIN line to its -----END line, then press Enter: ",
			want: true,
		},
		{
			// Pairing's own question, in lower case, on the line after the code
			// the phone shows.
			name: "the pairing code prompt",
			out:  "the phone shows AB-CD-EF\ntype that code: ",
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
		{
			// A colon that is not a question: no space, so no prompt. The help
			// cobra and these commands print is full of these, and reading them
			// as questions would make the harness cry wolf on every leaf.
			name: "a heading that ends in a bare colon",
			out:  "Usage:\n  fugaro version [flags]\n\nFlags:\n  -h, --help   help for version\n",
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
