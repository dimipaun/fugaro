package cli

// apply_enforce_test.go is AP9's half of the composite design
// (docs/composite-commands/implementation-plan.md, the section "AP9:
// enforcement"): the two checks that keep the rule true now that the tasks that
// built it have landed.
//
// The first is the census of the commands themselves. management_commands.golden
// holds every command D1566 calls a management command, with the change it
// makes, the steps the table declares for that change, and what a run of the
// command without --no-apply ends on in this package's own fakes. The commands
// D1566 leaves out are not here, and the golden's header names them with the
// cloud write that is why. The test runs each row: the steps the change owes
// run in process, the run files nothing for `fugaro apply`, and the state the
// command's own report claims is read off the cloud fake the step wrote into
// and never off the absence of a line.
//
// The second is the text lint. No message of this package hands one of the
// commands F11's audit names — `fugaro update`, `fugaro config publish`,
// `fugaro project policy publish`, `fugaro image build`, `fugaro image refresh`
// — to the reader as a step of their own. It is F11's own file set (the
// package's non-test sources) and F11's own grep, widened to the shapes the
// audit names by hand: a directive ("next: fugaro update", "run `fugaro image
// build`") and a claim that the command carries the change the message is about
// ("`fugaro update` publishes it"). What the audit left is recorded in
// testdata/next_step_allow.golden, and the ratchet is strict in both
// directions: a phrasing the tree says that the file does not list fails, and a
// line for a phrasing the tree has moved past fails too.

import (
	"context"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// cliPackageDir is this package's own directory, absolute. It is resolved once
// because the rigs move the process's working directory into a checkout
// (t.Chdir), and a relative path read after that names a directory inside it
// rather than the package's own sources.
var cliPackageDir = func() string {
	dir, err := filepath.Abs(".")
	if err != nil {
		panic(err)
	}
	return dir
}()

// nextStepAllowPath is where the audit's residue is recorded, one site per
// line, sorted: the file, a tab, and the phrasing.
var nextStepAllowPath = func() string {
	path, err := filepath.Abs("testdata/next_step_allow.golden")
	if err != nil {
		panic(err)
	}
	return path
}()

// updateNextStepAllow regenerates that file. A second flag.Bool of the same
// name panics, so the name is this test's alone.
var updateNextStepAllow = flag.Bool("update-next-step-allow", false, "rewrite testdata/next_step_allow.golden")

// nextStepFollowedAlt is F11's own list of the commands a message must not hand
// to the reader as a step of their own (reconciliation.md F11:
// `git grep -lE "fugaro (update|config publish|project policy publish|image
// build|image refresh)"`). The commands are matched as they are spelled.
const nextStepFollowedAlt = `fugaro (?:update|config publish|project policy publish|image build|image refresh)`

// nextStepShapes are the three ways a message hands one of them over.
//
// The directive shape is a word that makes the command the reader's next move
// ("next: fugaro update", "then run `fugaro update`", "re-run `fugaro config
// publish`") within one sentence of the command's own name. The claim shape is
// the command named as what carries the change the message is about ("`fugaro
// update` publishes it", "fugaro update writes this"), which is a next step
// said as a fact about the change rather than as an instruction. The hand-off
// shape is the audit's own fourth word, `with`: the command named as the means
// by which the reader gets what the sentence is about ("Start a rebuild with
// fugaro image build", "publish it again yourself, from a copy, with fugaro
// config publish"), which is an instruction spelled as the instrument of the
// act rather than as a verb of its own.
//
// All three are narrower than "mentions the command", which is the whole of F11's
// grep and would flag every description of what a stage does ("the stages
// `fugaro update` runs"): the audit's question is whether a user-facing message
// sends the reader to run one of these by hand.
var nextStepShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:next|then|now|run|rerun|re-run)\b[^\n]{0,60}?` + nextStepFollowedAlt),
	regexp.MustCompile(`(?i)` + nextStepFollowedAlt + `[^\n]{0,20}?\b(?:publishes|writes|applies|deploys)\s+(?:it|this|that|them|these|your)\b`),
	// `with` introduces the command directly rather than within a sentence of
	// it: the instrument is the command itself, and a wider window would catch
	// every text that merely mentions two things in one sentence.
	regexp.MustCompile(`(?i)\bwith\s+` + nextStepFollowedAlt),
}

// nextStepSite is one message that phrases one of them: the file it is in and
// the phrasing read out of it.
type nextStepSite struct {
	file   string
	phrase string
}

// line is the site as the golden spells it.
func (s nextStepSite) line() string { return s.file + "\t" + s.phrase }

// nextStepSites reads every string literal of this package's non-test sources —
// F11's own file set, so that a word in a comment or an identifier is not a
// message — and answers every site that phrases one of the commands.
//
// A file is read through the parser rather than by a text match so that a
// literal is a literal: the lint is about what a command prints, and a comment
// explaining the rule (this file is full of them) is not a message.
func nextStepSites(t *testing.T) []nextStepSite {
	t.Helper()
	entries, err := os.ReadDir(cliPackageDir)
	if err != nil {
		t.Fatalf("reading %s: %v", cliPackageDir, err)
	}
	var out []nextStepSite
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files++
		path := filepath.Join(cliPackageDir, name)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		var found []string
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, re := range nextStepShapes {
				if m := re.FindString(text); m != "" {
					found = append(found, nextStepPhrase(m))
				}
			}
			return true
		})
		slices.Sort(found)
		for _, phrase := range slices.Compact(found) {
			out = append(out, nextStepSite{file: name, phrase: phrase})
		}
	}
	if files < 100 {
		// A parse that silently read nothing would make every check below pass
		// on a tree it never looked at.
		t.Fatalf("only %d sources were read under %s: the F11 file set is the package's non-test sources, which are many more", files, cliPackageDir)
	}
	slices.SortFunc(out, func(a, b nextStepSite) int {
		if a.file != b.file {
			return strings.Compare(a.file, b.file)
		}
		return strings.Compare(a.phrase, b.phrase)
	})
	return out
}

// nextStepPhrase is a match as the golden records it: one line, with the
// quoting a message uses around a command's name taken out, so that "run
// `fugaro update`" and "run 'fugaro update" are one phrasing rather than three.
// What is left is the words and the command, which is what a reader of the file
// has to judge.
func nextStepPhrase(match string) string {
	line := strings.Join(strings.Fields(match), " ")
	return strings.NewReplacer("`", "", "'", "", `"`, "").Replace(line)
}

// TestNoMessageHandsOverAFollowedUpCommand is the lint: every phrasing of a
// followed-up command that this package's messages still carry is in the
// golden, and every line of the golden is still a phrasing the tree says.
//
// It is the strictness in both directions that makes the file worth reading. A
// new message that hands one over — a command that writes and still tells the
// reader to run `fugaro update`, a help text that promises a later publish —
// fails on its first run, and the fix is the command's own chain (AP4-AP7) and
// not a line in this file. A line whose text is gone fails too, so the file
// only shrinks and every line in it is a text somebody read and left.
func TestNoMessageHandsOverAFollowedUpCommand(t *testing.T) {
	measured := nextStepSites(t)
	if *updateNextStepAllow {
		nextStepAllowWrite(t, measured)
		return
	}
	recorded := nextStepAllowGolden(t)
	measuredLines := make(map[string]bool, len(measured))
	for _, site := range measured {
		measuredLines[site.line()] = true
		if !recorded[site.line()] {
			t.Errorf("%s phrases %q: a user-facing message may not send the reader to a command that is a follow-up of a change fugaro makes itself (spec, acceptance: no user-facing message tells the user to run publish or update by hand). Run the step from the command that made the change, or, when the text is one the audit left, record it in %s with -update-next-step-allow",
				site.file, site.phrase, nextStepAllowPath)
		}
	}
	for line := range recorded {
		if measuredLines[line] {
			continue
		}
		t.Errorf("%s records %q and no message says it any more: delete the line — a golden line that passes fails this test", nextStepAllowPath, line)
	}
}

// TestThePatternCatchesTheShapesTheAuditNames is the other half of the lint:
// the two shapes the audit calls out by name are caught by the pattern, and a
// text that merely describes what a command does is not. Without the first half
// a pattern narrowed to "run `fugaro (...)`" would pass the ratchet above on a
// tree full of "next: fugaro update"; without the second the lint would be a
// ban on naming the commands at all, which every description of a stage does.
func TestThePatternCatchesTheShapesTheAuditNames(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
		why  string
	}{
		{"next: fugaro update", true, "the hand-off the audit names by hand"},
		{"fugaro: the pools are published. next: fugaro update", true, "the same line inside a longer message"},
		{"`fugaro update` publishes it.", true, "the claim the audit names by hand"},
		{"it is written here and fugaro update writes it", true, "the same claim without the quoting"},
		{"run `fugaro image build` and then `fugaro apply`", true, "a directive"},
		{"re-run `fugaro config publish", true, "a directive of another command of the list"},
		{"then run 'fugaro project policy publish'", true, "a directive inside a sentence"},
		{"your next fugaro update applies them", true, "a statement about the installation's next run, which the audit read as the same shape"},
		{"`fugaro update` re-deploys this", true, "the claim with a prefixed verb"},
		{"it always does). Start a rebuild with fugaro image build.", true, "the hand-off the audit spells as \"with ... fugaro image build\": an instrument rather than a directive verb"},
		{"Publish it again yourself, from a copy, with fugaro config publish", true, "the same shape at the end of a longer sentence"},
		{"the stages `fugaro update` runs are the stage list", false, "a description of what the command does"},
		{"the image map is published by `fugaro update`", false, "the command as the agent of a passive sentence about the state"},
		{"fugaro update is the whole upgrade", false, "the CLAUDE.md rule, quoted in a help text"},
	} {
		got := false
		for _, re := range nextStepShapes {
			if re.MatchString(tc.text) {
				got = true
				break
			}
		}
		if got != tc.want {
			t.Errorf("%q: caught = %v, want %v (%s)", tc.text, got, tc.want, tc.why)
		}
	}
}

// nextStepAllowGolden is the recorded set, one line per site.
func nextStepAllowGolden(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(nextStepAllowPath)
	if err != nil {
		t.Fatalf("reading %s: %v (regenerate it with -update-next-step-allow)", nextStepAllowPath, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no site: a ledger that records nothing would pass this test on a tree that hands every command to the reader", nextStepAllowPath)
	}
	return out
}

// nextStepAllowHeader is the file's own comment block. It is written by the
// regeneration rather than kept beside it, so the file a run rewrites is the
// file a reader reads.
const nextStepAllowHeader = `# Every user-facing text of internal/cli that still phrases one of the commands
# F11's audit names as the reader's own next step, one per line and sorted: the
# file, a tab, and the phrasing read out of it.
#
# The audit (docs/composite-commands/reconciliation.md F11, run by AP7) read
# every such text and classified it. Nothing of class (a) is here — a chain a
# management command now runs itself — because those texts were removed by the
# command that took the chain over, and a text of that class left in the tree
# fails TestNoMessageHandsOverAFollowedUpCommand. What is left is:
#
#   (b) a stale-installation fix, which stays. fugaro update is the whole
#       upgrade (CLAUDE.md) and the commands that find an installation, a
#       checkout or a published node behind are the ones that tell a person to
#       run it: doctor's Fixes, init and setup's closing lines, update's own
#       report, the policy and image doors whose state a run has to be brought
#       to. AP7 classified these, and the hard rule keeps them.
#   (c) a text that names the command as what it is, without handing it to the
#       reader as a step: a claim about the state ("fugaro update re-deploys
#       this"), a sentence about what a command does, a line the reader of a
#       diagnostic is told the meaning of.
#
# A phrasing the tree says and this file does not list fails the test, and a
# line here whose text is gone fails too: the file is the audit's residue, and a
# line the tree has moved past is a line nobody re-read.
#
# Generated by
#   go test ./internal/cli -run TestNoMessageHandsOverAFollowedUpCommand -update-next-step-allow
`

// nextStepAllowWrite rewrites the golden from the measurement.
func nextStepAllowWrite(t *testing.T, measured []nextStepSite) {
	t.Helper()
	var b strings.Builder
	b.WriteString(nextStepAllowHeader)
	for _, site := range measured {
		fmt.Fprintln(&b, site.line())
	}
	if err := os.WriteFile(nextStepAllowPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("writing %s: %v", nextStepAllowPath, err)
	}
	files := map[string]bool{}
	for _, site := range measured {
		files[site.file] = true
	}
	t.Logf("%s: %d sites across %d files", nextStepAllowPath, len(measured), len(files))
}

// The three answers a row of management_commands.golden can record for the run
// of a management command. They are not three degrees of the same thing: what
// separates them is who runs the change's steps and where the record stands
// afterwards.
const (
	// managementLive: the command ran the steps its change owes, in process,
	// and nothing is owed afterwards (spec rules 1-3).
	managementLive = "live"
	// managementOwed: a step did not finish — in a hermetic world, a step that
	// needs something only a person at a terminal or a real cloud can give it —
	// and the change is saved with its steps in this machine's record, said in
	// D1563's words (spec rule 4).
	managementOwed = "owed"
	// managementNone: the change declares no step at all and the command is its
	// own whole answer — it writes what it changes in one act (a removal that
	// takes the jobs and the node with it), it is itself the step (the policy
	// publish), or the record its change is read in belongs to another project,
	// whose own `fugaro update` is what publishes it (the member's half of a
	// workspace membership, workspaceHandoff).
	managementNone = "none"
)

// managementCommand is one row of testdata/management_commands.golden.
type managementCommand struct {
	// command is the command line as a person runs it, "fugaro" included.
	command string
	// change is what the command changes, in the table's own spelling — a
	// setting key or a resource type — and the zero value for a command whose
	// change the table has nothing to say about (one that declares no step).
	change apply.Change
	// steps is what the table declares for that change, as the golden records
	// it: what the ratchet compares the table against.
	steps []string
	// end is one of the three answers above.
	end string
	// why says what the row's `end` means for this command, and what the run of
	// it is read back against.
	why string
}

// managementCommandsPath is the census of the management commands, absolute for
// the reason the lint's own file is: the rigs move the working directory.
var managementCommandsPath = func() string {
	path, err := filepath.Abs("testdata/management_commands.golden")
	if err != nil {
		panic(err)
	}
	return path
}()

// managementGolden reads the census, one row per command.
func managementGolden(t *testing.T) []managementCommand {
	t.Helper()
	data, err := os.ReadFile(managementCommandsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", managementCommandsPath, err)
	}
	var out []managementCommand
	for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("%s:%d holds %d fields, want 5 (command, change, steps, end, why): %q", managementCommandsPath, i+1, len(fields), line)
		}
		row := managementCommand{command: fields[0], end: fields[3], why: fields[4]}
		if fields[1] != "-" {
			kind, key, ok := strings.Cut(fields[1], ":")
			if !ok {
				t.Fatalf("%s:%d spells the change %q: want <kind>:<key> or -", managementCommandsPath, i+1, fields[1])
			}
			row.change = apply.Change{Kind: apply.Kind(kind), Key: key}
		}
		if fields[2] != "-" {
			row.steps = strings.Split(fields[2], ", ")
		}
		out = append(out, row)
	}
	if len(out) < 15 {
		// A census that read nothing would pass every check below on a tree it
		// never looked at, which is the one way a test like this fails quietly.
		t.Fatalf("%s holds %d rows: the management command set is D1566's whole list", managementCommandsPath, len(out))
	}
	return out
}

// TestEveryManagementCommandDeclaresItsSteps is the census's static half: every
// row of the golden names a command this build has, and the table declares the
// steps the row records — the one place a change's steps live (spec rule 2,
// D1561), read here rather than restated.
//
// The comparison runs both ways. The table answering a different list than the
// golden records fails, because the golden is what a reviewer read when the
// command's chain landed, and every step it names has to be one this build can
// run (apply.KnownStep): a step name nothing can run is a change saved with a
// step nobody can answer.
func TestEveryManagementCommandDeclaresItsSteps(t *testing.T) {
	for _, row := range managementGolden(t) {
		switch row.end {
		case managementLive, managementOwed, managementNone:
		default:
			t.Errorf("%s records the end %q, which is not one of %s, %s and %s", row.command, row.end, managementLive, managementOwed, managementNone)
		}
		path := strings.Fields(strings.TrimPrefix(row.command, "fugaro "))
		cmd, rest, err := NewRootCmd().Find(path)
		if err != nil || cmd == nil || len(rest) != 0 || cmd.CommandPath() != row.command {
			t.Errorf("%s is not a command of this build (%v, %v): the census holds the commands D1566 lists, and one this build does not have records a chain nobody can run", row.command, err, rest)
			continue
		}
		if row.change.Key == "" {
			if len(row.steps) != 0 {
				t.Errorf("%s records no change and the steps %v: a command whose change the table does not answer for owes none", row.command, row.steps)
			}
			continue
		}
		steps, ok := apply.Steps(row.change)
		if !ok {
			t.Errorf("%s: the table declares nothing for %s %q, so the change would be saved with nothing to make it live (spec rule 2)", row.command, row.change.Kind, row.change.Key)
			continue
		}
		if !slices.Equal(steps, row.steps) {
			t.Errorf("%s: %s %q declares %v and the golden records %v", row.command, row.change.Kind, row.change.Key, steps, row.steps)
		}
		for _, name := range steps {
			if !apply.KnownStep(name) {
				t.Errorf("%s owes %q, which is neither an initflow stage nor a catalog build: nothing can run it", row.command, name)
			}
		}
	}
}

// managementRun is one row's own run: the world it is run in and the read-back
// that says what the run did.
type managementRun struct {
	// args is the argv, without "fugaro".
	args []string
	// stdin is what the run types. A management command's chain asks nothing
	// (D1540), but the write it makes can: an enrolment is confirmed by the
	// project's name, an account token is pasted.
	stdin string
	// owner says the run stands at the owner's own terminal — stdinIsTerminal
	// answers yes and the input is handed out a byte at a time, as a terminal
	// delivers it — which is how this package's rigs drive the writes that read
	// one (r.enrol, r.add, r.rm).
	owner bool
	// pending is what the row's own record holds afterwards, and the default is
	// this machine's own document, bound to the fixtures' project. A row whose
	// record belongs to another project — the workspace's own — names it itself.
	pending func(*testing.T) []string
	// check reads the row's claim back off the fake cloud: what makes "the
	// change is live" a measurement rather than the absence of a line.
	check func(t *testing.T, out, errOut string, err error)
}

// managementRunArgs runs one row.
func managementRunArgs(t *testing.T, run managementRun) (string, string, error) {
	t.Helper()
	if run.owner {
		ownerAtTheTerminal(t)
		return executeOwnerStdin(t, run.stdin, run.args...)
	}
	return executeStdin(t, run.stdin, run.args...)
}

// TestEveryManagementCommandEndsWithNothingPending is the spec's own test: each
// command of the census is run in this package's fakes, without --no-apply, and
// what it leaves behind is read the way `fugaro apply` reads it.
//
// A `live` row ends with nothing owed and with no sentence sending the reader
// anywhere else, and its own read-back says the change really reached the cloud
// the step writes — a chain that skipped a step and said nothing would pass a
// check that only read the record. An `owed` row ends with D1563's sentence
// over a non-zero exit and exactly the steps the golden lists in this machine's
// record: a step that cannot finish in a hermetic world is not a row that may
// end silently, and what it owes has to be the table's own list. A `none` row
// declares no step, so nothing may be owed for it either.
func TestEveryManagementCommandEndsWithNothingPending(t *testing.T) {
	for _, row := range managementGolden(t) {
		t.Run(row.command, func(t *testing.T) {
			world, ok := managementWorlds[row.command]
			if !ok {
				t.Fatalf("%s: this test has no world to run it in, so the census would record a chain nothing exercised", row.command)
			}
			run := world(t)
			if want := strings.Fields(row.command)[1:]; len(run.args) < len(want) || !slices.Equal(run.args[:len(want)], want) {
				t.Fatalf("%s: the world runs %v, which is not this command", row.command, run.args)
			}
			out, errOut, err := managementRunArgs(t, run)
			all := out + errOut
			forbidden := []string{"saved, not live yet", "still owed", "fugaro apply"}
			pending := run.pending
			if pending == nil {
				pending = func(t *testing.T) []string { return pendingSteps(record(t)) }
			}
			switch row.end {
			case managementLive:
				if err != nil {
					t.Fatalf("%s did not finish its own chain: %v\n%s", row.command, err, all)
				}
				for _, text := range forbidden {
					if strings.Contains(all, text) {
						t.Errorf("%s answers %q over a change whose steps it ran:\n%s", row.command, text, all)
					}
				}
				if got := pending(t); len(got) != 0 {
					t.Errorf("%s left %v owed, want nothing: the change runs its own steps", row.command, got)
				}
			case managementNone:
				if err != nil {
					t.Fatalf("%s did not finish: %v\n%s", row.command, err, all)
				}
				for _, text := range forbidden {
					if strings.Contains(all, text) {
						t.Errorf("%s answers %q, and its change declares no step of this machine's to run:\n%s", row.command, text, all)
					}
				}
				if got := pending(t); len(got) != 0 {
					t.Errorf("%s left %v owed, and its change declares no step", row.command, got)
				}
			case managementOwed:
				if err == nil {
					t.Fatalf("%s reported its change live in a world where a step cannot finish:\n%s", row.command, all)
				}
				if got := pending(t); !slices.Equal(got, row.steps) {
					t.Errorf("%s left %v owed, want the change's own steps %v", row.command, got, row.steps)
				}
			}
			if run.check != nil {
				run.check(t, out, errOut, err)
			}
		})
	}
}

// managementWorlds is the fixture each row of the census is run in, keyed by
// the command line the golden spells. Every row has one: the test refuses a row
// with no world rather than recording a chain it never ran.
var managementWorlds = map[string]func(*testing.T) managementRun{
	"fugaro settings set":           managementWorldSettingsSet,
	"fugaro settings unset":         managementWorldSettingsUnset,
	"fugaro image add":              managementWorldImageAdd,
	"fugaro image rm":               managementWorldImageRm,
	"fugaro accounts add":           managementWorldAccountsAdd,
	"fugaro accounts rm":            managementWorldAccountsRm,
	"fugaro project repos add":      managementWorldReposAdd,
	"fugaro project repos rm":       managementWorldReposRm,
	"fugaro project sync":           managementWorldProjectSync,
	"fugaro models add":             managementWorldModelsAdd,
	"fugaro models rm":              managementWorldModelsRm,
	"fugaro workspace add":          managementWorldWorkspaceAdd,
	"fugaro workspace rm":           managementWorldWorkspaceRm,
	"fugaro workspace cap set":      managementWorldWorkspaceCapSet,
	"fugaro project policy publish": managementWorldPolicyPublish,
}

// managementWorldSettingsSet is the hook's own fixture (afterWriteFixture): an
// installation at a version-3 node the policy stage migrates and publishes
// over, whose config states an orchestrator ceiling the node does not carry —
// so the write is a change and not a first publish, and the version the node
// moves to is a measurement of the stage having run.
func managementWorldSettingsSet(t *testing.T) managementRun {
	_, db := afterWriteFixture(t)
	return managementRun{
		args: []string{"settings", "set", "orchestrator.max_running", "9", "--layer", "local"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			if v := nodeVersionOn(t, db); v != projectpolicy.Version {
				t.Errorf("the node is version %d, want the %d the stage migrates it to: the chain's policy step did not run", v, projectpolicy.Version)
			}
			if got := publishedOrchestrator(t, db, "max_running"); got != "9" {
				t.Errorf("the node's orchestrator.max_running is %q, want the 9 the command wrote", got)
			}
		},
	}
}

// managementWorldSettingsUnset is the same world's other direction: the key the
// fixture's config carries is taken out of it, and the stage the write owes
// publishes the file as it now stands.
func managementWorldSettingsUnset(t *testing.T) managementRun {
	_, db := afterWriteFixture(t)
	return managementRun{
		args: []string{"settings", "unset", "orchestrator.max_running", "--layer", "local"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			// The key is gone and the block it lived in may be left empty: what
			// the unset promises is the key, not the parent.
			if got := configOnDisk(t); strings.Contains(got, "max_running") {
				t.Errorf("the file still states the key the unset took out:\n%s", got)
			}
			if v := nodeVersionOn(t, db); v != projectpolicy.Version {
				t.Errorf("the node is version %d, want the %d the stage migrates it to: the unset ran no step", v, projectpolicy.Version)
			}
		},
	}
}

// publishedOrchestrator is one key of the published node's orchestrator block,
// spelled rather than compared as a number: the fake hands a value back in
// whatever type it decoded it into.
func publishedOrchestrator(t *testing.T, db *gcpfake.RTDB, key string) string {
	t.Helper()
	node, ok := db.Value(projectpolicy.PathShared()).(map[string]any)
	if !ok {
		t.Fatalf("the node at %s is %T, want the published document", projectpolicy.PathShared(), db.Value(projectpolicy.PathShared()))
	}
	block, ok := node["orchestrator"].(map[string]any)
	if !ok {
		t.Fatalf("the node's orchestrator block is %T, want the published one: %v", node["orchestrator"], node)
	}
	return fmt.Sprint(block[key])
}

// managementWorldImageAdd is AP4's world: an applied installation with the
// Cloud Build fake, the runs bucket the build's record lives in and a checkout
// of the listed repository, so the entry's build and the redeploy after it both
// have somewhere to land.
func managementWorldImageAdd(t *testing.T) managementRun {
	_, fb := imageAddChainWorld(t)
	df := entryDockerfile(t, catalogBuildInline)
	return managementRun{
		args: []string{"image", "add", "base", "--inline", df, "--default"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			if n := buildPosts(fb); n != 1 {
				t.Errorf("image add submitted %d builds, want the one entry it added", n)
			}
			image := catalogBuildImage(t, "base")
			if fb.Tag(image, "latest") == "" {
				t.Errorf("no build of %s was promoted: the chain never built the entry", image)
			}
			deployed := deployedImage(t, "app")
			if entry, ok := gcp.CatalogImageEntry(deployed); !ok || entry != "base" {
				t.Errorf("the deployed job's image is %q, which is not the catalog image of base: the redeploy did not follow the entry", deployed)
			}
		},
	}
}

// managementWorldImageRm is the removal in the same world: the entry has to
// exist to be taken out, so the world adds one through the command's own path
// first — and it is an entry of its own (`tools`) rather than the default one,
// because removing the entry the deployed job runs is refused (an image a job
// still points at is not one this command takes away), which is a refusal with
// its own test and not this row's question.
func managementWorldImageRm(t *testing.T) managementRun {
	r, _ := imageAddChainWorld(t)
	// The removal reads the checkout's own config before it takes an entry away
	// (catalogPointedAt), and that read resolves the project layer — a bucket the
	// chain world's config names as gs:// and which this package's TestMain
	// refuses to open for real. The layer is empty here, which is the state every
	// fixture in this package is in (init_anchor_test.go's own override).
	oldOpen := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = oldOpen })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+t.TempDir())
	}
	df := entryDockerfile(t, catalogBuildInline)
	if out, errOut, err := execute(t, "image", "add", "tools", "--inline", df); err != nil {
		t.Fatalf("image add in the removal's own world: %v\n%s", err, out+errOut)
	}
	// The addition's own repository step anchored the checkout
	// (`gcp_project` in its fugaro.yaml, the write that run tells the person to
	// commit), and the removal's redeploy refuses a working tree that is not the
	// default branch's until that is committed. The world takes that step the
	// way the run's own words say to (enrolLanded), which is what a person
	// running the two commands a moment apart does.
	enrolLanded(t)
	return managementRun{
		args: []string{"image", "rm", "tools"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			if got := configOnDisk(t); strings.Contains(got, "name: tools") {
				t.Errorf("the entry is still in this machine's config:\n%s", got)
			}
			// And the redeploy the removal owes ran: the checkout's repositories
			// were planned again without the entry (D1574).
			if len(r.ran(t, "plan")) == 0 {
				t.Errorf("the removal ran no terraform plan: the checkout was not redeployed without the entry")
			}
		},
	}
}

// managementWorldAccountsAdd is the account's own converge (F10): the
// declaration, the secret and the installation's job set, in one act.
func managementWorldAccountsAdd(t *testing.T) managementRun {
	r := newAddRig(t)
	ownerAtTheTerminal(t)
	return managementRun{
		args:  []string{"accounts", "add", "team-a", "--repos", "owner/repo-a"},
		stdin: addToken() + "\n",
		owner: true,
		check: func(t *testing.T, out, _ string, err error) {
			if err != nil {
				return
			}
			if _, ok := r.addLocal(t).ClaudeAccounts["team-a"]; !ok {
				t.Errorf("the account's own declaration is not in the config the converge wrote: %v", r.addLocal(t).ClaudeAccounts)
			}
			if !strings.Contains(out, "claude_accounts.team-a") {
				t.Errorf("the account's own block does not name the declaration it wrote:\n%s", out)
			}
		},
	}
}

// managementWorldAccountsRm is AP6's removal: the account is in the config and
// the secret store, and the two steps that make the removal live — the node
// that stops naming it and the installation whose job set it owned — run here.
func managementWorldAccountsRm(t *testing.T) managementRun {
	r := newAddRig(t)
	r.add(t, "team-a", "--repos", "owner/repo-a")
	return managementRun{
		args: []string{"accounts", "rm", "team-a", "--profile", "standard"},
		check: func(t *testing.T, out, _ string, err error) {
			if err != nil {
				return
			}
			if _, ok := r.addLocal(t).ClaudeAccounts["team-a"]; ok {
				t.Errorf("the account is still in the config the removal wrote: %v", r.addLocal(t).ClaudeAccounts)
			}
			if got := accountsAddRow(t, out, "account"); !strings.Contains(got, "claude_accounts.team-a removed") {
				t.Errorf("the block's account line reads %q, want the declaration it removed named", got)
			}
		},
	}
}

// managementWorldReposAdd is AP6's enrolment: the installation, the checkout
// the repository root is converged from and the node the admission is published
// in, all in the fake terraform and the fake database.
func managementWorldReposAdd(t *testing.T) managementRun {
	r := enrolRig(t)
	return managementRun{
		args:  []string{"project", "repos", "add", enrolRepo},
		stdin: initProjectName + "\n",
		owner: true,
		check: func(t *testing.T, out, errOut string, err error) {
			if err != nil {
				return
			}
			if _, ok := r.addLocal(t).Repos[enrolRepo]; !ok {
				t.Errorf("the enrolment's own write is not in the config it read back")
			}
			if !strings.Contains(out+errOut, "fugaro run --repo "+enrolEntry+" is ready") {
				t.Errorf("the enrolment does not end on its own report:\n%s", out+errOut)
			}
		},
	}
}

// managementWorldReposRm is the removal: RE4's inverse converge, which takes
// the local entry, the published admission, the jobs and the allow-list entry
// away itself — one act, in one command.
func managementWorldReposRm(t *testing.T) managementRun {
	r := rmRigFor(t, "", rmGone{})
	return managementRun{
		args:  []string{"project", "repos", "rm", rmEntryA},
		stdin: initProjectName + "\n",
		owner: true,
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			// The store the change is about, read where a read finds it: the
			// removed repository is off the allow-list and the other one is not.
			if got := allowReposOf(t, r.db); !slices.Equal(got, []string{rmEntryB}) {
				t.Errorf("allow_repos = %v after the removal, want %v", got, []string{rmEntryB})
			}
			if _, ok := r.rmLocal(t).Repos[rmRepoA]; ok {
				t.Errorf("the removal left its own repos: entry in this machine's config")
			}
		},
	}
}

// managementWorldProjectSync is the sync's own act: it offers the provider's
// repositories and writes the accepted ones into the published allow-list
// through the same ETag-guarded write `project repos add` uses.
func managementWorldProjectSync(t *testing.T) managementRun {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	newSyncRig(t, "acme/new")
	return managementRun{
		args: []string{"project", "sync", "--github-app-id", "12345", "--github-app-key"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			if got := allowReposOf(t, f.db); !slices.Contains(got, "github/acme/new") {
				t.Errorf("allow_repos = %v, want the provider's repository added: the sync wrote no node", got)
			}
		},
	}
}

// managementWorldModelsAdd is AP6's model write: the price the command puts in
// the config reaches the node the project's runs are admitted by, in process.
func managementWorldModelsAdd(t *testing.T) managementRun {
	f := modelsChainFixture(t)
	managementPublishFirst(t)
	return managementRun{
		args: []string{"models", "add", modelsChainID, "--price-in", "1", "--price-out", "2"},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			price, ok := publishedPolicy(t, f).Prices[modelsChainID]
			if !ok || price.InputPerM == nil || *price.InputPerM != 1 {
				t.Errorf("the node prices %s at %+v, want the rates the command was given", modelsChainID, price)
			}
		},
	}
}

// managementWorldModelsRm is the same write's other direction: the model the
// command added is taken out of the config and out of the node.
func managementWorldModelsRm(t *testing.T) managementRun {
	f := modelsChainFixture(t)
	managementPublishFirst(t)
	if out, errOut, err := execute(t, "models", "add", modelsChainID, "--price-in", "1", "--price-out", "2"); err != nil {
		t.Fatalf("models add in the removal's own world: %v\n%s", err, out+errOut)
	}
	return managementRun{
		args: []string{"models", "rm", modelsChainID},
		check: func(t *testing.T, _, _ string, err error) {
			if err != nil {
				return
			}
			if price, ok := publishedPolicy(t, f).Prices[modelsChainID]; ok {
				t.Errorf("the node still prices %s at %+v after the removal", modelsChainID, price)
			}
		},
	}
}

// managementPublishFirst publishes the node as the fixture stands, so that the
// only difference a models change carries is its own entry: the first publish
// would otherwise write the whole document and a chain that ran nothing would
// still show a change.
func managementPublishFirst(t *testing.T) {
	t.Helper()
	if out, errOut, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatalf("project policy publish in the fixture's own world: %v\n%s", err, out+errOut)
	}
}

// managementWorldWorkspaceAdd is the member's half of a membership: the pin and
// the membership key are its own record, live once the file is written, and
// what carries them to the runs is the project's own `fugaro update` (R3, R4) —
// no step of this machine's apply can publish another project's file.
func managementWorldWorkspaceAdd(t *testing.T) managementRun {
	_, path := workspaceWriteHome(t)
	return managementRun{
		args:    workspaceAddArgs(),
		pending: func(t *testing.T) []string { return workspaceOwedFor(t, "aurora", "aurora-gcp") },
		check: func(t *testing.T, out, _ string, err error) {
			if err != nil {
				return
			}
			if !strings.Contains(readFileString(t, path), "workspace: workspace-alpha\n") {
				t.Errorf("the membership was not written:\n%s", readFileString(t, path))
			}
			if !strings.Contains(out, "next: fugaro update") {
				t.Errorf("the write does not name the command that carries it to the runs:\n%s", out)
			}
		},
	}
}

// managementWorldWorkspaceRm is the same half's other direction: the membership
// is taken out of the file, and the record it fed is again the project's own
// update to publish.
func managementWorldWorkspaceRm(t *testing.T) managementRun {
	_, path := workspaceWriteHome(t)
	if out, errOut, err := execute(t, workspaceAddArgs()...); err != nil {
		t.Fatalf("workspace add in the removal's own world: %v\n%s", err, out+errOut)
	}
	if !strings.Contains(readFileString(t, path), "workspace: workspace-alpha\n") {
		t.Fatalf("the world's own membership was not written:\n%s", readFileString(t, path))
	}
	return managementRun{
		args:    []string{"workspace", "rm", "workspace-alpha"},
		pending: func(t *testing.T) []string { return workspaceOwedFor(t, "aurora", "aurora-gcp") },
		check: func(t *testing.T, out, _ string, err error) {
			if err != nil {
				return
			}
			if strings.Contains(readFileString(t, path), "workspace: workspace-alpha\n") {
				t.Errorf("the membership is still in the file:\n%s", readFileString(t, path))
			}
		},
	}
}

// managementWorldWorkspaceCapSet is the cap the workspace's own project must be
// applied for: the grant reaches its database and the record its service reads
// is published there, so the change is saved and both steps stay owed.
func managementWorldWorkspaceCapSet(t *testing.T) managementRun {
	_, _, owner := workspaceOwnerHome(t)
	workspaceAppend(t, owner, "workspace_cap:\n  monthly: 40\n")
	return managementRun{
		args:    []string{"workspace", "cap", "set", "--workspace", "workspace-alpha", "--monthly", "50"},
		pending: func(t *testing.T) []string { return workspaceOwedFor(t, "workspace-alpha", "ws-gcp") },
		check: func(t *testing.T, out, _ string, err error) {
			workspaceAssertSavedNotLive(t, "workspace cap set", out, err)
		},
	}
}

// managementWorldPolicyPublish is the one command of the set that is itself the
// step (D1566): it publishes the node in place and owes nothing, records
// nothing and names no other command.
func managementWorldPolicyPublish(t *testing.T) managementRun {
	f := newProjectFixture(t, "")
	return managementRun{
		args: []string{"project", "policy", "publish"},
		check: func(t *testing.T, out, _ string, err error) {
			if err != nil {
				return
			}
			if len(publishedPolicy(t, f).AllowRepos) == 0 {
				t.Errorf("the publish wrote no allow-list, so nothing was published")
			}
			if _, err := os.Stat(recordPath(t)); err == nil {
				t.Errorf("the publish recorded a step for later:\n%s", readFileString(t, recordPath(t)))
			}
			if strings.Contains(out, "next:") {
				t.Errorf("the publish, which is the step, names a next command:\n%s", out)
			}
		},
	}
}
