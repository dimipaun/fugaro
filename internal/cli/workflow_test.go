package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// `fugaro workflow add|rm` (D1177, docs/config-management/spec.md §3.7): the
// block's existence and nothing of what it holds. What the tests below are for:
//
//   - the two commands write and take away the block, and neither touches a
//     block it was not asked about (TestWorkflowAddCreatesTheBlock,
//     TestWorkflowRmRemovesTheBlock, TestWorkflowRmRemovesOneWorkflowOfSeveral);
//   - the executable-key refusal (config.ExecutableKeys) is the layer publish's
//     own, in both directions and at the block's own keys
//     (TestWorkflowAddRefusesAnExecutableKeyWithoutTheOwnersFlag and the three
//     beside it) — and the loader is the settings path's own, so a block that
//     would not read back is refused whatever its keys are
//     (TestWorkflowAddRefusesABlockThatWouldNotReadBack);
//   - a session and a managed project both reach it, because a repository's
//     fugaro.yaml is the one document a session may write and the gate is not
//     read for it (TestWorkflowAddIsAllowedInAnAgentSession,
//     TestWorkflowRepoWriteInAManagedProjectReadsNoMark), while the owner's own
//     terminal in a managed project reads the gate and files its verdict
//     (TestWorkflowAddInAManagedProjectFilesTheAdmissionAtTheOwnersTerminal);
//   - `config init` still writes the minimal file, which is D1177's second half
//     (TestDocsConfigInitStillWritesAMinimalFile), and this group is what puts a
//     workflow into the file a repository is anchored with — which is the write
//     path's own read that file does not satisfy yet
//     (TestWorkflowAddRefusesAFileTheSettingsPathCannotRead).
//
// Every fixture here is one config.Parse accepts, because every write this
// group makes is held to that loader (settingsReparse) and because the loader
// is what a repository's file has to satisfy: at least one workflow, and
// commands.build and commands.test in each of them. A block is created with its
// keys, so `workflow add` carries them.

// workflowDocs is a second workflow's block for settingsRepoYAML's `web`,
// indented two spaces so it lands under `workflows:`.
const workflowDocs = "  docs:\n    commands:\n      build: make docs\n      test: make test\n"

// workflowDocsWithImage is a block whose image steps are what makes removing it
// an executable change.
const workflowDocsWithImage = "  docs:\n    commands:\n      build: make docs\n      test: make test\n    image:\n      apt: [curl]\n      setup: [sh setup.sh]\n"

// workflowAddDocs creates the docs block in the file the write lands in, with
// the flag the commands it carries require.
func workflowAddDocs(t *testing.T, extra ...string) (string, string, error) {
	t.Helper()
	args := append([]string{"workflow", "add", "docs",
		"--set", "commands.build=make docs",
		"--set", "commands.test=make test",
		"--set", "resources.cpu=4",
		"--executable-changes"}, extra...)
	out, _, err := execute(t, args...)
	return out, "", err
}

// TestWorkflowAddCreatesTheBlock is the plan's red step: the group does not
// exist before it. The block it creates is the keys --set names, under the
// name, appended to the document — every byte the file already held is still
// there, which is what a create is and what an edit is not.
func TestWorkflowAddCreatesTheBlock(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	out, _, err := workflowAddDocs(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "create workflows.docs in "+path) {
		t.Errorf("the run does not say what it wrote:\n%s", out)
	}
	if !strings.Contains(out, "=== EXECUTABLE CHANGES") {
		t.Errorf("the run does not show the keys that run as shell:\n%s", out)
	}
	if !strings.Contains(out, "next: fugaro update") {
		t.Errorf("the write does not end with the next step every settings write names:\n%s", out)
	}
	got := readFileString(t, path)
	if !strings.HasPrefix(got, before) {
		t.Errorf("the write did not leave the bytes the file already held alone:\n%s", got)
	}
	cfg, ps := config.Parse([]byte(got))
	if len(ps) > 0 {
		t.Fatalf("the file does not read back after the write: %s", ps[0].String())
	}
	wf, ok := cfg.Workflows["docs"]
	if !ok || wf.Commands.Build != "make docs" || wf.Commands.Test != "make test" || wf.Resources.CPU != 4 {
		t.Errorf("workflows.docs = %+v, want build 'make docs', test 'make test' and cpu 4", wf)
	}
	if _, ok := cfg.Workflows["web"]; !ok {
		t.Errorf("the write dropped the workflow the file already had:\n%s", got)
	}
}

// TestWorkflowRmRemovesTheBlock is the plan's other red step. The block goes,
// the `workflows:` key with it when it was the last one under it, and the file
// is left byte for byte the one the other block's own create would have
// written.
func TestWorkflowRmRemovesTheBlock(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(workflowDocs))

	out, _, err := execute(t, "workflow", "rm", "docs", "--executable-changes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "remove workflows.docs from "+path) {
		t.Errorf("the run does not say what it removed:\n%s", out)
	}
	if got, want := readFileString(t, path), settingsRepoYAML(""); got != want {
		t.Errorf("fugaro.yaml = %q, want the file without the block %q", got, want)
	}
}

// TestWorkflowRmRemovesOneWorkflowOfSeveral: the block named goes and no other
// does, whatever the two hold — a comment and a key beside it are the bytes a
// block-level removal is easiest to lose.
func TestWorkflowRmRemovesOneWorkflowOfSeveral(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("  # the docs site, built by make\n  docs:\n    commands:\n      build: make docs\n      test: make test\n")+
		"  api:\n    commands:\n      build: go build ./cmd/api\n      test: go test ./internal/api/...\n")

	if _, _, err := execute(t, "workflow", "rm", "docs", "--executable-changes"); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, path)
	if strings.Contains(got, "docs") || strings.Contains(got, "make docs") {
		t.Errorf("the removal left the block it named:\n%s", got)
	}
	for _, keep := range []string{
		"  web:\n    commands:\n      test: go test ./...\n      build: go build ./...\n",
		"  api:\n    commands:\n      build: go build ./cmd/api\n      test: go test ./internal/api/...\n",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("the removal did not leave %q as it was:\n%s", keep, got)
		}
	}
	if cfg, ps := config.Parse([]byte(got)); len(ps) > 0 {
		t.Errorf("the file does not read back: %s", ps[0].String())
	} else if len(cfg.Workflows) != 2 {
		t.Errorf("the file reads back as %d workflows, want the two the removal did not name", len(cfg.Workflows))
	}
}

// TestWorkflowAddRefusesAnExecutableKeyWithoutTheOwnersFlag is the plan's
// refusal, at the flag config publish's own --executable-changes is: the key is
// named, the flag is named, and the file is byte for byte what it was. The
// document it guards is the one that decides what runs as shell in every run of
// this repository (SEC).
func TestWorkflowAddRefusesAnExecutableKeyWithoutTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	out, _, err := execute(t, "workflow", "add", "docs",
		"--set", "commands.build=make docs", "--set", "commands.test=make test")
	if err == nil {
		t.Fatal("workflow add wrote commands.build without --executable-changes")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "--executable-changes") {
		t.Errorf("the refusal does not name the owner's flag: %v", err)
	}
	if !strings.Contains(out, "workflows.docs.commands.build") {
		t.Errorf("the run does not name the executable key it would write:\n%s", out)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the file")
	}
}

// TestWorkflowAddWritesAnExecutableKeyWithTheOwnersFlag is the other half of
// that refusal, so a gate that refused every executable key whatever the flag
// says fails here rather than passing the refusal above.
func TestWorkflowAddWritesAnExecutableKeyWithTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))

	out, _, err := workflowAddDocs(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := readFileString(t, path)
	if !strings.Contains(got, "build: make docs") || !strings.Contains(got, "test: make test") {
		t.Errorf("fugaro.yaml does not carry the write:\n%s", got)
	}
}

// TestWorkflowRmRefusesAnExecutableBlockWithoutTheOwnersFlag: taking a block
// away is a change to the keys that run as shell in it, and the comparison is
// config publish's own, which reports a dropped key the same way it reports a
// written one.
func TestWorkflowRmRefusesAnExecutableBlockWithoutTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(workflowDocs))
	before := readFileString(t, path)

	out, _, err := execute(t, "workflow", "rm", "docs")
	if err == nil {
		t.Fatal("workflow rm removed a block of commands without --executable-changes")
	}
	if !strings.Contains(err.Error(), "--executable-changes") {
		t.Errorf("the refusal does not name the owner's flag: %v", err)
	}
	if !strings.Contains(out, "workflows.docs.commands.build") {
		t.Errorf("the run does not name the executable keys it would take away:\n%s", out)
	}
	if readFileString(t, path) != before {
		t.Error("the refused removal changed the file")
	}

	if _, _, err := execute(t, "workflow", "rm", "docs", "--executable-changes"); err != nil {
		t.Fatalf("the removal is not reachable with the owner's flag: %v", err)
	}
	if got := readFileString(t, path); strings.Contains(got, "docs:") {
		t.Errorf("the flagged removal did not land:\n%s", got)
	}
}

// TestWorkflowRmRefusesABlockOfImageStepsWithoutTheOwnersFlag is the SEC
// sentence at the keys this group's own --set cannot write: a block's
// image.apt and image.setup are lists, so `settings set --add` writes them, and
// what this test pins is the other half — the edit that takes such a block away
// carries them, and the comparison is driven by config.ExecutableKeys rather
// than by the keys a scalar write can name.
func TestWorkflowRmRefusesABlockOfImageStepsWithoutTheOwnersFlag(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(workflowDocsWithImage))
	before := readFileString(t, path)

	out, _, err := execute(t, "workflow", "rm", "docs")
	if err == nil {
		t.Fatal("workflow rm removed a block of image steps without --executable-changes")
	}
	for _, key := range []string{"workflows.docs.image.apt", "workflows.docs.image.setup"} {
		if !strings.Contains(out, key) {
			t.Errorf("the run does not name %s among the executable changes it would make:\n%s", key, out)
		}
	}
	if readFileString(t, path) != before {
		t.Error("the refused removal changed the file")
	}
	if _, _, err := execute(t, "workflow", "rm", "docs", "--executable-changes"); err != nil {
		t.Fatalf("the removal is not reachable with the owner's flag: %v", err)
	}
	if got := readFileString(t, path); strings.Contains(got, "apt") {
		t.Errorf("the flagged removal did not land:\n%s", got)
	}
}

// TestWorkflowRmRefusesTheLastWorkflow: a repository's file must define at
// least one workflow (config.Validate), and every settings write holds the
// document it would leave to that loader — so the last block is one this
// command cannot take away, in the loader's own words and with nothing written.
// The rule is not this group's: a file with no workflow takes one from the
// project layer, which the loader the write path reads the bytes with does not
// have.
func TestWorkflowRmRefusesTheLastWorkflow(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "workflow", "rm", "web", "--executable-changes")
	if err == nil {
		t.Fatal("workflow rm left a repository with no workflow")
	}
	if !strings.Contains(err.Error(), "must define at least one workflow") {
		t.Errorf("the refusal is not the loader's own rule: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused removal changed the file")
	}
}

// TestWorkflowAddRefusesABlockThatWouldNotReadBack: the loader is the settings
// path's (settingsReparse), so a block that would not load is refused with the
// loader's sentence rather than written — a workflow carries its commands, and
// a repository whose file defines none cannot run.
func TestWorkflowAddRefusesABlockThatWouldNotReadBack(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "workflow", "add", "docs", "--set", "resources.cpu=4")
	if err == nil {
		t.Fatal("workflow add wrote a block no run can use")
	}
	if !strings.Contains(err.Error(), "would not read back") || !strings.Contains(err.Error(), "commands.build") {
		t.Errorf("the refusal is not the loader's own, naming what the block lacks: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed the file")
	}
}

// TestWorkflowAddRefusesABlockTheFileHolds: add creates a block and never edits
// one, which is what `settings set workflows.NAME.<key>` is for — and the
// refusal says so rather than writing over a block a person may have
// hand-edited.
func TestWorkflowAddRefusesABlockTheFileHolds(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "workflow", "add", "web", "--set", "resources.cpu=4")
	if err == nil {
		t.Fatal("workflow add wrote over a block the file holds")
	}
	if !strings.Contains(err.Error(), "settings set workflows.web.") {
		t.Errorf("the refusal does not name the command that writes a block's keys: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused add changed the file")
	}
}

// TestWorkflowRmRefusesANameTheFileDoesNotHold: a removal of nothing is refused
// rather than reported as a write that happened.
func TestWorkflowRmRefusesANameTheFileDoesNotHold(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "workflow", "rm", "docs", "--executable-changes")
	if err == nil || !strings.Contains(err.Error(), "holds no workflows.docs block") {
		t.Fatalf("err = %v, want the sentence that says the file holds no such block", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused removal changed the file")
	}
}

// TestWorkflowAddRefusesWhatTheWritePathRefuses: this group adds no second key
// lookup, so a key `settings set` refuses is refused here in the same words,
// and a value the loader refuses is refused by the same loader.
func TestWorkflowAddRefusesWhatTheWritePathRefuses(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	for _, tc := range []struct {
		what string
		args []string
		says string
	}{
		{"a key no setting has", []string{"nosuch=1"}, "no setting has that name"},
		{"a key of the block no row names", []string{"commands.nosuch=1"}, "no setting has that name"},
		{"a key outside the block", []string{"nosuch.deep=1"}, "no setting has that name"},
		{"a value the loader refuses", []string{"image.key_files=go.mod"}, "would not read back"},
		{"a set with no value", []string{"commands.build"}, "FIELD=VALUE"},
		{"a set with no field", []string{"=make"}, "FIELD=VALUE"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			before := readFileString(t, path)
			args := append([]string{"workflow", "add", "docs", "--set"}, tc.args...)
			_, _, err := execute(t, args...)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want a refusal naming %q", err, tc.says)
			}
			if readFileString(t, path) != before {
				t.Error("the refused write changed the file")
			}
		})
	}
}

// TestWorkflowAddRefusesANameTheLoaderRefuses, and one it would have to invent
// a block for: `add NAME` with no --set has nothing to create the block with,
// and a name the file could not define is refused with the rule
// (config.WorkflowNameRE) rather than by a reparse that names a path.
func TestWorkflowAddRefusesANameTheFileCouldNotDefine(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	before := readFileString(t, path)

	_, _, err := execute(t, "workflow", "add", "docs")
	if err == nil || !strings.Contains(err.Error(), "--set") {
		t.Fatalf("err = %v, want a refusal naming --set", err)
	}
	for _, name := range []string{"", "web.x", "web x", "Docs", "nine-teen-chars-xxx"} {
		if _, _, err := execute(t, "workflow", "add", name, "--set", "resources.cpu=4"); err == nil {
			t.Errorf("%q was taken as a workflow name", name)
		}
	}
	if readFileString(t, path) != before {
		t.Error("a refused add changed the file")
	}
}

// TestWorkflowAddIsAllowedInAnAgentSession: a repository's own fugaro.yaml is
// the one document a coding agent's session may write (the fence's own rule,
// settings_write.go), and this group writes exactly that document — which is
// what the plan's §5 row admits.
func TestWorkflowAddIsAllowedInAnAgentSession(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	agentSession(t)

	out, _, err := workflowAddDocs(t)
	if err != nil {
		t.Fatalf("a session was refused a repository's own file: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "build: make docs") {
		t.Errorf("the session's write did not land:\n%s", got)
	}
}

// TestWorkflowRepoWriteInAManagedProjectReadsNoMark: in a managed project, a
// repository-scoped write reads no mark and files no record — the gate is asked
// about the documents it judges, and a repository's fugaro.yaml is one the
// fence already decides (settingsGateAsks). The mutation this catches is a gate
// read bolted onto this group for no reason: it would spend a bucket read on
// every workflow edit and file verdicts about a document no rule of the gate
// reads.
func TestWorkflowRepoWriteInAManagedProjectReadsNoMark(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	agentSession(t)

	out, _, err := workflowAddDocs(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "build: make docs") {
		t.Errorf("the write did not land:\n%s", got)
	}
	if reqs := r.gcs.Requests(); len(reqs) != 0 {
		t.Errorf("a repository-scoped write paid %d calls to read marks:\n%v", len(reqs), reqs)
	}
	if recs := r.audits(t); len(recs) != 0 {
		t.Errorf("a repository-scoped write filed %+v", recs)
	}
}

// TestWorkflowAddInAManagedProjectFilesTheAdmissionAtTheOwnersTerminal: the
// owner's own terminal is where the gate IS read for a repository's file, and
// the admission is recorded before the run reports that the write landed
// (D1014, M6) — so an edit that applied and left no trail fails this test.
func TestWorkflowAddInAManagedProjectFilesTheAdmissionAtTheOwnersTerminal(t *testing.T) {
	r := newSettingsManagedRig(t, settingsMarksManaged)
	r.managedRunsBucket(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(""))
	noAgentSession(t)
	fakeTerminal(t)

	out, _, err := executeStdin(t, "", "workflow", "add", "docs",
		"--set", "commands.build=make docs", "--set", "commands.test=make test", "--executable-changes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "build: make docs") {
		t.Errorf("the write did not land:\n%s", got)
	}
	assertNoPrompt(t, out)
	if !strings.Contains(out, initflow.GateProceeded()) {
		t.Errorf("the run does not say what decided the step:\n%s", out)
	}
	if recs := r.audits(t); len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Errorf("the day holds %+v, want the one admission of this write", recs)
	}
}

// TestWorkflowAddRefusesAFileTheSettingsPathCannotRead: this group writes
// through the settings write path, so the file it writes into is the one that
// path can read — and the read holds a repository's fugaro.yaml to
// config.Parse with no project layer (settingsReadDocument). Two shapes a real
// repository has fall outside it: a file that defines no workflow at all (the
// file `config init` anchors a repository with, which takes its workflow from
// the project layer's default_profile, or gets its first one from `setup`), and
// a file whose workflow names a profile (the layered repository §3.7 is about).
// Neither is a rule of this command, and both are refused before a byte is
// written; the test is here so the boundary is visible in the suite rather than
// only in a report.
func TestWorkflowAddRefusesAFileTheSettingsPathCannotRead(t *testing.T) {
	for _, tc := range []struct {
		what string
		yaml string
		says string
	}{
		{
			"the anchored file with no workflow",
			"version: 1\nproject: aurora\ngcp_project: proj-1234\ngit:\n  provider: github\n",
			"must define at least one workflow",
		},
		{
			"a workflow that names a profile",
			"version: 1\nproject: aurora\ngcp_project: proj-1234\ngit:\n  provider: github\nworkflows:\n  web:\n    profile: svc\n    commands:\n      build: go build ./...\n      test: go test ./...\n",
			"profiles come from the project layer",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			settingsWriteHome(t)
			path := settingsWriteCheckout(t, tc.yaml)

			out, _, err := workflowAddDocs(t)
			if err == nil {
				t.Fatalf("a file the settings path cannot read took a workflow:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal is not the read's own: %v", err)
			}
			if readFileString(t, path) != tc.yaml {
				t.Error("the refused write changed the file")
			}
		})
	}
}

// TestWorkflowHelpOffersOnlyKeysARepositoryFileCanHold is the boundary above read
// from the other side: the keys the help prints as examples have to be keys this
// write takes, and `profile` is not one of them — a repository's file naming a
// profile is exactly the file the settings read refuses, so an example printing it
// is a command the CLI declines with the loader's own sentence. The examples are
// also where a person writes their first block, and the keys the suite itself
// writes are the ones the examples print (workflowAddDocs,
// TestWorkflowAddCreatesTheBlock), so this is one artifact and not a second list.
func TestWorkflowHelpOffersOnlyKeysARepositoryFileCanHold(t *testing.T) {
	for _, tc := range []struct {
		command string
		help    string
	}{
		{"workflow", newWorkflowCmd().Long},
		{"workflow add", newWorkflowAddCmd().Long},
	} {
		if strings.Contains(tc.help, "profile") {
			t.Errorf("`fugaro %s`'s help names profile: a repository's file that names one is a file this write path's read refuses (profiles come from the project layer), so it is not a key of a block a repository holds", tc.command)
		}
		if !strings.Contains(tc.help, "commands.test") {
			t.Errorf("`fugaro %s`'s help prints no example of a block's own key, and its examples are what a person copies:\n%s", tc.command, tc.help)
		}
	}
}

// TestDocsConfigInitStillWritesAMinimalFile is D1177's second half
// (docs/config-management/spec.md §3.7): the file a repository is anchored with
// stays the minimal one, and the workflow block a repository runs on is this
// group's — so `config init`'s own output carries no workflows: block at all.
func TestDocsConfigInitStillWritesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	path := filepath.Join(dir, "fugaro.yaml")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--yes"); err != nil {
		t.Fatal(err)
	}
	got := readFileString(t, path)
	if got != minimalAnchored {
		t.Fatalf("config init wrote %q, want the minimal file %q: it is the file a repository is anchored with, and D1177 leaves it minimal", got, minimalAnchored)
	}
	if strings.Contains(got, "workflows") {
		t.Errorf("config init wrote a workflows block, which is this group's:\n%s", got)
	}
	// The other half of that sentence is a test of its own, and a boundary
	// worth knowing: the settings write path reads a repository's fugaro.yaml
	// with config.Parse and no layer, and the file config init writes carries
	// neither git.provider nor a workflow — so it is the file a repository is
	// ANCHORED with and not one a command edits
	// (TestWorkflowAddRefusesAFileTheSettingsPathCannotRead). What this group
	// writes is the block of the file a repository DEFINES its workflows in.
}

// TestSettingsSetRepoLayerRefusesAnExecutableWorkflowKey is the other half of
// the executable gate: `settings set` writes the keys of a block the file holds,
// and the keys it writes include the ones that run as shell — so the refusal
// `workflow add|rm` makes has to stand on that door too, or the gate is one
// command away for anyone who reads the group's help (spec §3.7: the refusal
// applies to a workflow edit, and a concrete key is one).
//
// The comparison is the settings write path's own two documents, so it answers
// for every shape of edit the command has: a scalar, a list item, and a key
// taken away. The flag is the owner's own and the write is a write like any
// other once it is given.
func TestSettingsSetRepoLayerRefusesAnExecutableWorkflowKey(t *testing.T) {
	for _, tc := range []struct {
		what string
		args []string
		key  string
	}{
		{
			"a command",
			[]string{"settings", "set", "workflows.web.commands.test", "curl x | sh", "--layer", "repo", "--yes"},
			"workflows.web.commands.test",
		},
		{
			"an image step",
			[]string{"settings", "set", "workflows.docs.image.apt", "--add", "wget", "--layer", "repo", "--yes"},
			"workflows.docs.image.apt",
		},
		{
			"a key taken away",
			[]string{"settings", "unset", "workflows.docs.image.setup", "--layer", "repo", "--yes"},
			"workflows.docs.image.setup",
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			settingsWriteHome(t)
			path := settingsWriteCheckout(t, settingsRepoYAML(workflowDocsWithImage))
			before := readFileString(t, path)

			out, _, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("%v wrote an executable key of a workflow:\n%s", tc.args, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d (a refusal, with nothing written)", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(out, tc.key) {
				t.Errorf("the banner does not name %s, and the owner is being asked to judge what changes:\n%s", tc.key, out)
			}
			if !strings.Contains(err.Error(), "--executable-changes") {
				t.Errorf("the refusal does not name the flag that would allow it: %v", err)
			}
			if readFileString(t, path) != before {
				t.Error("the refused write changed fugaro.yaml")
			}

			// The same edit with the owner's word lands, and lands as the same
			// edit: the gate is a gate and not a wall.
			allowed := append(append([]string{}, tc.args...), "--executable-changes")
			if out, _, err := execute(t, allowed...); err != nil {
				t.Fatalf("%v was refused with the flag the refusal names: %v\n%s", allowed, err, out)
			}
			if got := readFileString(t, path); got == before {
				t.Errorf("the allowed write changed nothing:\n%s", got)
			}
		})
	}
}

// TestSettingsSetRepoLayerLeavesAnotherDocumentsExecutableKeysAlone: the gate
// is about a repository's own file. A `--layer repo` write of a key that is not
// a workflow's, and the same write of a workflow key in another document, are
// neither of them a change to what a run of THIS repository executes, and both
// are written with no flag asked for.
func TestSettingsSetRepoLayerLeavesAnotherDocumentsExecutableKeysAlone(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML(workflowDocsWithImage))

	// git.base_branch beside a block full of executable keys: nothing of the
	// block moves, so nothing is asked.
	if out, _, err := execute(t, "settings", "set", "git.base_branch", "develop", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("a write that changes no command was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "base_branch: develop") {
		t.Errorf("the write did not land:\n%s", got)
	}
	// A workflow key that is not executable: the gate is config.ExecutableKeys'
	// list and not "a key under workflows".
	if out, _, err := execute(t, "settings", "set", "workflows.web.resources.cpu", "4", "--layer", "repo", "--yes"); err != nil {
		t.Fatalf("a workflow key that runs nothing was refused: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "cpu: 4") {
		t.Errorf("the write did not land:\n%s", got)
	}
}

// TestWorkflowAddWritesTheCheckoutThePathNames: --repo promises "a path inside
// its checkout", and the file the edit lands in has to be that checkout's own
// fugaro.yaml. A machine that remembers another clone of the same repository
// must not take the remembered one for the one that was named, and a clone it
// remembers nothing about is a checkout this command writes all the same — the
// reduction to owner/name knows which repository it is and not which clone the
// person typed.
func TestWorkflowAddWritesTheCheckoutThePathNames(t *testing.T) {
	testutil.IsolateGit(t)
	settingsWriteHome(t)
	enrollRepo(t, "aurora", "acme/app")
	enrollRepo(t, "aurora", "acme/other")
	remembered := repoArgCheckout(t, filepath.Join(t.TempDir(), "remembered"), "https://github.com/acme/app.git")
	named := repoArgCheckout(t, filepath.Join(t.TempDir(), "named"), "https://github.com/acme/app.git")
	fresh := repoArgCheckout(t, filepath.Join(t.TempDir(), "fresh"), "https://github.com/acme/other.git")
	for _, dir := range []string{remembered, named, fresh} {
		if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(settingsRepoYAML("")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Two clones of one repository, and the machine remembers the one the
	// write must NOT land in.
	if err := localcfg.RecordCheckout(os.Getenv, "aurora", localcfg.CheckoutIdentity{Provider: "github", Repo: "acme/app"}, remembered, time.Now(), checkoutTempRoots()); err != nil {
		t.Fatal(err)
	}
	rememberedBefore := readFileString(t, filepath.Join(remembered, "fugaro.yaml"))
	// Nothing in the working directory may be what the write falls back on.
	t.Chdir(t.TempDir())

	for _, tc := range []struct{ what, dir string }{
		{"the second clone of a remembered repository", named},
		{"a clone this machine remembers nothing about", fresh},
	} {
		t.Run(tc.what, func(t *testing.T) {
			out, _, err := execute(t, "workflow", "add", "docs",
				"--set", "commands.build=make docs",
				"--set", "commands.test=make test",
				"--executable-changes", "--repo", tc.dir)
			if err != nil {
				t.Fatalf("workflow add --repo %s was refused: %v\n%s", tc.dir, err, out)
			}
			if got := readFileString(t, filepath.Join(tc.dir, "fugaro.yaml")); !strings.Contains(got, "docs:") {
				t.Errorf("the named checkout's fugaro.yaml does not hold the block:\n%s", got)
			}
		})
	}
	if readFileString(t, filepath.Join(remembered, "fugaro.yaml")) != rememberedBefore {
		t.Error("the write landed in a remembered clone of the repository that was not the one named")
	}
}
