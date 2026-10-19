package cli

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/watch"
	"github.com/spf13/cobra"
)

// The release notes of 0.9.0 (docs/releases/v0.9.0.md) are the Highlights the
// release page publishes, and they are the one document nothing else in the
// tree reads: a claim in them that the tree does not hold is a claim no
// compiler, no schema and no other test can catch, and the file is written
// once, before the tag, by someone reading a branch. These tests are the pin.
// The first holds the file to the format docs/releases/README.md states (3 to
// 6 user bullets, `###` headings and no `##`, the two section titles the
// website's changelog check recognises, each operator step labelled with the
// place it is run in). The second holds each claim about this release to the
// fact in the tree that makes it true — the Go symbol that builds the name, the
// cobra command that carries the flag, the shipped gate that decides whether the
// feature is usable at all — rather than to a substring of the sentence: a
// claim whose symbol moves fails here rather than shipping on the release page.
const releaseNotesV090 = "../../docs/releases/v0.9.0.md"

// releaseNotesSections are the two headings docs/releases/README.md names, and
// website/src/lib/changelog.mjs recognises, in full: any other `###` line in a
// release file is text the site renders as written and its own check refuses.
var releaseNotesSections = []string{"For operators", "Known limits"}

// readReleaseNotesV090 reads the file and fails with its path, so a moved or
// deleted file names itself rather than one of the assertions below.
func readReleaseNotesV090(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(releaseNotesV090)
	if err != nil {
		t.Fatalf("reading %s: %v (the release notes are a required artifact from 0.4.0 on: scripts/release.sh and the release workflow both refuse without them)", releaseNotesV090, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("%s is empty; the release workflow refuses to publish an empty one", releaseNotesV090)
	}
	return string(data)
}

// TestReleaseNotesV090FollowsTheFormat holds the file to docs/releases/README.md:
// 3 to 6 bullets for users before the first section, both section headings
// written as `###` (a `##` inside the file would be a second top-level heading
// beside GoReleaser's own `## Highlights`), no other heading, and the
// operators' steps numbered from 1 in order, each one labelled with the place
// it is run in.
func TestReleaseNotesV090FollowsTheFormat(t *testing.T) {
	lines := strings.Split(readReleaseNotesV090(t), "\n")

	var bullets int
	var headings []string
	var steps []string
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "### "):
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")))
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			continue
		case strings.HasPrefix(trimmed, "## "), trimmed == "##", strings.HasPrefix(trimmed, "# "):
			t.Errorf("%s carries the top-level heading %q: the file's headings are `###`, because GoReleaser puts the file under its own `## Highlights`", releaseNotesV090, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if section == "" {
				bullets++
			}
			continue
		}
		if section == "For operators" && releaseNotesStepRE.MatchString(trimmed) {
			steps = append(steps, trimmed)
		}
	}

	if bullets < 3 || bullets > 6 {
		t.Errorf("%s has %d bullets for users; docs/releases/README.md asks for 3 to 6", releaseNotesV090, bullets)
	}
	for _, h := range headings {
		if h != releaseNotesSections[0] && h != releaseNotesSections[1] {
			t.Errorf("%s carries the heading %q; the two a release file may carry are %q and %q, and the site's changelog check refuses any other", releaseNotesV090, h, releaseNotesSections[0], releaseNotesSections[1])
		}
	}
	for _, want := range releaseNotesSections {
		if !strings.Contains(strings.Join(headings, "\n"), want) {
			t.Errorf("%s has no `### %s` section", releaseNotesV090, want)
		}
	}
	if len(steps) == 0 {
		t.Fatalf("%s has no numbered step under `### For operators`", releaseNotesV090)
	}
	for i, step := range steps {
		if want := strings.TrimSpace(releaseNotesStepRE.FindString(step)); want != strconv.Itoa(i+1)+"." {
			t.Errorf("the operators' steps are out of order at %q: want step %d, got %q", step, i+1, want)
		}
		if !releaseNotesStepPlace(step) {
			t.Errorf("the operators' step %q names no place it is run in: every command is **per machine**, **per repo checkout** or **per installation**, because the three are different objects and a reader who runs one where another belongs changes the wrong thing", step)
		}
	}
	// The shared project jobs run the installation's base image, which only a
	// re-apply of the installation moves: `fugaro upgrade` and `fugaro image
	// refresh` never touch them (internal/cli/image_refresh_steps.go moves the
	// check job and each repository's own clone jobs). So the rollout ends with
	// that re-apply, per installation, after the images are rebuilt — without
	// it a shared job keeps a runner that refuses the policy just published
	// (the review of PR #660).
	last := steps[len(steps)-1]
	if !strings.Contains(last, "**Per installation") || !strings.Contains(last, "`fugaro init --firebase <gcp-project>`") {
		t.Errorf("the operators' last step is %q: it must be the per-installation `fugaro init --firebase <gcp-project>` that moves the shared jobs onto the base image the image step pinned, since no image command touches them", last)
	}
	refresh := -1
	for i, step := range steps {
		if refresh < 0 && strings.Contains(step, "`fugaro image refresh`") {
			refresh = i
		}
	}
	if refresh < 0 || refresh >= len(steps)-1 {
		t.Errorf("the operators' steps rebuild the images (`fugaro image refresh`) at step %d of %d: the shared jobs' re-apply must come after it", refresh+1, len(steps))
	}
}

// releaseNotesStepRE is a numbered step of the operators' list, and its number
// is what the order is read from.
var releaseNotesStepRE = regexp.MustCompile(`^(\d+)\. `)

// releaseNotesStepLabelRE is the first bold span of a step, which is where the
// place it is run in is written.
var releaseNotesStepLabelRE = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// releaseNotesStepPlaces are the three places this release's steps
// distinguish, because the three are different objects: the CLI is installed
// per machine, the plugin pin and the images are per repository checkout, and
// the installation's cloud resources are per installation. A label may say
// more than the place — "**Per installation, in your own terminal**" is the
// place and how to run it — so the place is a prefix of the label rather than
// the whole of it.
var releaseNotesStepPlaces = []string{"Per machine", "Per repo checkout", "Per installation"}

func releaseNotesStepPlace(step string) bool {
	m := releaseNotesStepLabelRE.FindStringSubmatch(step)
	if m == nil {
		return false
	}
	for _, place := range releaseNotesStepPlaces {
		if strings.HasPrefix(m[1], place) {
			return true
		}
	}
	return false
}

// TestReleaseNotesV090ClaimsHoldInTheTree pins each claim the notes make about
// this release to the fact in the tree that makes it true. Each pair is
// deliberate: the sentence is asserted beside the symbol, command or constant
// the claim rests on, so a claim and its fact cannot drift apart in either
// direction — a feature that moves to another release fails here because the
// notes still say 0.9.0, and a sentence rewritten while its fact stands fails
// here because the text it was checked against is gone.
//
// Nothing here is a claim checked against itself, and nothing is matched as a
// bare word: the object path comes from the function that builds it, the label
// from the constants that write it, the profile key from the struct's own YAML
// tag, the console keys from the CLI structs that write them, the operator steps
// and the `--by` value from cobra's definitions, and the managed and Batch
// paragraphs from the shipped gates the commands refuse on.
//
// Mutation (verified, each planted and reverted): flip `cloneShipped` to false;
// change `sealSince` to "0.8.5"; change `token.ObjectKey`'s last segment; drop
// the row naming 0.9.0 from projectpolicy.VersionSince; rename
// managed.ProjectLabelKey; rename the `claude_accounts` YAML tag; remove
// `launched_by`'s json tag from internal/watch.JSONRun; remove `--local` from fugaro
// upgrade; move `fugaro managed flag` under another parent; set
// `userStatsByUser` to "person"; rename the watch table's BY column; delete the
// sealed token's object name from the first bullet; drop a step's
// `**Per installation**` label; add two bullets so the file carries eight;
// write a section as `## Known limits`; write "Managed projects return" back
// into the highlights; rewrite the project-export, provision-batch and
// scoped-role limits into descriptions of the commands; and replace the base
// image's pin with the copy it stopped being — each of which fails here or in
// TestReleaseNotesV090ManagedWordingMatchesTheGates.
func TestReleaseNotesV090ClaimsHoldInTheTree(t *testing.T) {
	notes := readReleaseNotesV090(t)
	names := func(s string) bool { return strings.Contains(notes, s) }

	// The seal: the notes name the object a sealed budget token is written at,
	// and internal/budget/token.ObjectKey is the name that object has — the
	// same constant the banner grants, the job's read-only condition and the
	// bucket's lifecycle rule are written from, and the envelope's additional
	// authenticated data. The release whose runner reads a sealed token is this
	// one (sealSince).
	object := token.ObjectKey("<slug>", "<run>")
	if !names("`" + object + "`") {
		t.Errorf("%s does not name the sealed token's object as the tree builds it (%q, internal/budget/token.ObjectKey): `tokens/` alone is not the name, and a reader who writes the object by hand has to have it", releaseNotesV090, object)
	}
	if sealSince != "0.9.0" {
		t.Errorf("sealSince is %q: the notes say the seal ships in 0.9.0, and the constant is the release whose runner reads a sealed token", sealSince)
	}

	// The clone gate: the notes say a `checkout: clone` workflow reaches a
	// shared job, which is true only while cloneShipped is true.
	if !names("`checkout: clone`") {
		t.Errorf("%s no longer names `checkout: clone`", releaseNotesV090)
	}
	if !cloneShipped {
		t.Errorf("cloneShipped is false, so a `checkout: clone` launch is refused on every image a release can build: %s may not say one reaches a shared job", releaseNotesV090)
	}

	// The policy version: the notes' rollout is a republish, and the version the
	// node moves to is the one this release introduced.
	if got := projectpolicy.VersionSince[projectpolicy.Version]; got != "0.9.0" {
		t.Errorf("projectpolicy.Version is %d and VersionSince names %q for it; the notes describe this release's rollout, so the version it publishes must be the one this release introduced",
			projectpolicy.Version, got)
	}
	if !names("`fugaro project policy publish --delete`") {
		t.Errorf("%s does not carry the republish the version bump needs (a publish refuses to overwrite a node written in another version)", releaseNotesV090)
	}

	// The named accounts: the key the notes tell an operator to write is the
	// one the local config parses, read off the struct's own YAML tag rather
	// than restated.
	if want := yamlTagOf(t, reflect.TypeOf(localcfg.Config{}), "ClaudeAccounts"); !names(want) {
		t.Errorf("%s does not name %s, the key the local config parses named Claude accounts from (internal/localcfg.Config.ClaudeAccounts)", releaseNotesV090, want)
	}

	// The managed project's second mark, as the writer spells it: the notes name
	// the label a managed project carries, and these two constants are what
	// managed.WriteManagedLabel and managed.WriteManagedLabelForFlag put on the
	// project.
	if want := managed.ProjectLabelKey + "=" + managed.ProjectLabelValue; !names(want) {
		t.Errorf("%s does not name %s, the label internal/infra/managed writes and the managed read requires (managed.ProjectLabelKey, managed.ProjectLabelValue)", releaseNotesV090, want)
	}

	// The windows and the breakdown of `fugaro runs stats`: the value the notes
	// pass to --by is the command's own, and the flag's default is that value —
	// a breakdown renamed in the command fails here rather than sending a
	// reader to `--by` with a word the command refuses.
	stats := commandAt(t, "runs", "stats")
	byFlag := stats.Flags().Lookup("by")
	if byFlag == nil {
		t.Fatalf("fugaro runs stats has no --by flag, and %s names `fugaro runs stats --by user`", releaseNotesV090)
	}
	if byFlag.DefValue != userStatsByUser {
		t.Errorf("fugaro runs stats' --by defaults to %q and userStatsByUser is %q: the notes and the command must agree on the breakdown", byFlag.DefValue, userStatsByUser)
	}
	if !names("`fugaro runs stats --by " + userStatsByUser + "`") {
		t.Errorf("%s does not name `fugaro runs stats --by %s`, the only breakdown the command publishes (userStatsByUser)", releaseNotesV090, userStatsByUser)
	}

	// The BY column: the notes say watch and runs ls name each run's launcher
	// under it, and watch's own plain frame is where the column's name is
	// written — the header a reader sees, not a word in these notes. runs ls'
	// own header is a literal in internal/cli (runs_test.go's header check), so
	// watch's rendered frame is the half a test can read without restating it.
	if !names("BY column") {
		t.Errorf("%s no longer says the launcher is shown in a BY column", releaseNotesV090)
	}
	if !watchHasBYColumn(t) {
		t.Errorf("fugaro watch's plain frame has no BY column (internal/watch/plain.go's RUN table header): %s offers one to its reader", releaseNotesV090)
	}

	// The five keys the console bullet names: they are this release's additions
	// to the documents `watch --json` and `runs ls --json` write, so they are
	// checked against the CLI structs that carry them (internal/watch's JSONRun
	// and JSONFinished. The console's half — that its own closed lists carry
	// these keys, and that a key they omit refuses a document whole — is pinned
	// by the console's test suite against these same structs; a Go test may not
	// read it, because internal/cli may not name a path under the console.
	runKeys := jsonKeysOf(t, reflect.TypeOf(watch.JSONRun{}))
	finishedKeys := jsonKeysOf(t, reflect.TypeOf(watch.JSONFinished{}))
	for _, key := range []string{"job", "profile", "provider", "account", "launched_by"} {
		if !names("`" + key + "`") {
			t.Errorf("%s does not name the console key %q this release added", releaseNotesV090, key)
		}
		if !runKeys[key] || !finishedKeys[key] {
			t.Errorf("%q is not a key of both internal/watch.JSONRun (%v) and JSONFinished (%v): a key the CLI writes and the console's list omits makes it refuse the whole document, and %s says these five no longer do",
				key, runKeys[key], finishedKeys[key], releaseNotesV090)
		}
	}

	// The operator steps: every command and flag they name is one the CLI
	// carries, by cobra's own definitions, so a step a reader cannot run fails
	// here. The place each is run in is the format test's business above.
	for _, c := range []struct{ path, flag string }{
		{"upgrade", "local"},
		{"upgrade", ""},
		{"init", "firebase"},
		{"init", "repo"},
		{"init", "create-project"},
		{"init", "managed"},
		{"doctor", ""},
		{"watch", ""},
		{"runs ls", ""},
		{"managed flag", ""},
		{"managed apply", ""},
		{"restore", ""},
		{"project policy show", ""},
		{"project policy publish", "delete"},
		{"project repos add", ""},
		{"project repos rm", ""},
		{"image refresh", ""},
	} {
		words := c.path
		cmd := commandAt(t, strings.Split(c.path, " ")...)
		if c.flag != "" {
			words += " --" + c.flag
			if cmd.Flags().Lookup(c.flag) == nil {
				t.Errorf("fugaro %s has no --%s flag, and %s tells an operator to run it", c.path, c.flag, releaseNotesV090)
			}
		}
		// The command must end where the notes' span or its argument does: a
		// bare prefix would let "`fugaro upgrade --local`" stand in for the bare
		// "`fugaro upgrade`" step, and "`fugaro init --repo`" for a longer flag.
		ends := []string{"`", " <", " ."}
		if c.flag != "" {
			ends = append(ends, " ")
		}
		found := false
		for _, end := range ends {
			found = found || names("`fugaro "+words+end)
		}
		if !found {
			t.Errorf("%s does not name `fugaro %s`, which the tree carries and this release's operators run", releaseNotesV090, words)
		}
	}

	// The commands that must NOT exist, because the notes say they do not ship:
	// `fugaro project export` is the known limit's own example of a promise this
	// release does not keep, and a command added under that name would make the
	// sentence false while every other check here still passed.
	if cmd := findCommand("project", "export"); cmd != nil {
		t.Errorf("fugaro project export exists (%q), and %s states it is not in this release", cmd.CommandPath(), releaseNotesV090)
	}
	if !names("`fugaro project export` is not in this release") {
		t.Errorf("%s does not state plainly that `fugaro project export` is not in this release", releaseNotesV090)
	}

	// The shipped gates. Both are shut in this tree (internal/cli/recipes_skew.go),
	// and a gate that is shut has to be said to be shut: the notes may not offer
	// a gated feature as something a reader can use, and the paragraphs that
	// carry the refusal are asserted here so the wording cannot quietly become a
	// description of the feature.
	if !managedDeployerShipped {
		if !names("Managed projects stay disabled") {
			t.Errorf("%s does not say managed projects stay disabled, and managedDeployerShipped is false: every command that would create a managed project or act as its deployer refuses before any cloud call", releaseNotesV090)
		}
		for _, gone := range []string{"Managed projects return", "managed projects return"} {
			if names(gone) {
				t.Errorf("%s says %q, and managedDeployerShipped is false: no managed apply runs at all in this release", releaseNotesV090, gone)
			}
		}
	}
	if !batchProvisionShipped {
		if !names("`fugaro init --provision-batch` refuses") {
			t.Errorf("%s does not say `fugaro init --provision-batch` refuses, and batchProvisionShipped is false", releaseNotesV090)
		}
		if cmd := commandAt(t, "init"); cmd.Flags().Lookup("provision-batch") == nil {
			t.Errorf("fugaro init has no --provision-batch flag, and %s describes what it does when it refuses (batchProvisionShipped is false: the flag stays, the provisioning refuses)", releaseNotesV090)
		}
	}

	// The images: this release's base is pinned, not copied, and the sentence
	// that said otherwise is one an earlier draft of these notes carried. The
	// base image stopped being copied before 0.9.0 — docs/release.md states it
	// in bold ("The base image is not copied any more") and internal/cli's
	// refresh resolves the release's `-pinned` tag, verifies the cosign
	// signature and pins the digest the installation's own remote repository
	// serves — so an operator step that describes a copy sends the reader to a
	// billable build that no longer happens.
	if !names("`-pinned` tag") || !names("nothing is copied") {
		t.Errorf("%s does not describe what `fugaro image refresh` does with the base image: it resolves this release's `-pinned` tag, verifies the cosign signature and pins the digest the installation's own remote repository serves (nothing is copied, and nothing is pushed)", releaseNotesV090)
	}
	for _, absent := range []string{"copies this release's base image", "copies the base image", "copy of the base image"} {
		if names(absent) {
			t.Errorf("%s says %q: the base image is not copied any more (docs/release.md, internal/cli/image_refresh.go), so the notes may not describe a copy", releaseNotesV090, absent)
		}
	}

	// The known limits: the checks this release cannot run, and the things it
	// deliberately does not claim. Each is a sentence a reader relies on, so the
	// wording that says it is pinned rather than the shape: a limit rewritten
	// into a feature is the one edit this list exists to refuse.
	for what, want := range map[string]string{
		"the sealed token's live check":  "Check 60",
		"the shared job's live check":    "Check 53",
		"the not-yet-run marker":         "NOT RUN",
		"the scoped deployer's absence":  "The deployer account, its create-and-read role and your impersonation grant are created as before",
		"the Batch backend still off":    "`fugaro init --provision-batch` refuses",
		"the project export not shipped": "`fugaro project export` is not in this release",
	} {
		if !names(want) {
			t.Errorf("%s does not say %s (%q) under `### Known limits`", releaseNotesV090, what, want)
		}
	}

	// The one direction the README states outright: a bullet is what a user can
	// do now, never the changelog's commit titles, and nothing internal-only.
	// The changelog's own prefixes are the words a bullet written from one
	// carries.
	for _, line := range strings.Split(notes, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		for _, prefix := range []string{"ci:", "chore:", "test:", "refactor:", "docs:"} {
			if strings.Contains(line, prefix) {
				t.Errorf("%s carries %q in a bullet: docs/releases/README.md asks for what a user can do now, not the commit titles the changelog already holds", releaseNotesV090, prefix)
			}
		}
	}
}

// TestReleaseNotesV090ManagedWordingMatchesTheGates is the other half of the
// managed paragraph: the notes name four commands as refusing, and with the
// shipped gates closed the CLI refuses exactly those, while `fugaro managed
// flag` — which the notes say runs — is not refused by the gate at all. An
// earlier draft of these notes offered `managed flag`, a `--managed` creation
// and a `managed apply` as things a user can do; this is the test that would
// have caught it, and the one a gate moved back onto the flag command fails.
//
// Mutation (verified, each planted and reverted): call
// refuseManagedNotShipped() from managedFlag; drop `fugaro managed apply` from
// the refusals the notes name; and drop `fugaro restore` from them.
func TestReleaseNotesV090ManagedWordingMatchesTheGates(t *testing.T) {
	closeShippedGates(t)
	notes := readReleaseNotesV090(t)

	// Each of these is refused by the gate, before any cloud call, and named in
	// the notes as a command that refuses: the reader is not left to discover it
	// at a terminal. The names are the spellings the notes use, so a step that
	// drops one is the failure.
	for _, c := range []struct {
		notes string
		args  []string
	}{
		{"`fugaro init --create-project --managed`", []string{"init", "--create-project", "--gcp-project", "fugaro-newproj-1234", "--managed", "--budget", "50", "--budget-currency", "USD", "--billing-account", "000000-000000-000000"}},
		{"`fugaro init --managed`", []string{"init", "--managed"}},
		{"`fugaro managed apply`", []string{"managed", "apply", "infra"}},
		{"`fugaro restore`", []string{"restore", "fugaro-newproj-1234", "infra"}},
	} {
		out, err := runGated(t, c.args...)
		if err == nil || !strings.Contains(err.Error(), managedDisabled) {
			t.Errorf("fugaro %s: exit %d (%v), want the user error %q\n%s", strings.Join(c.args, " "), ExitCode(err), err, managedDisabled, out)
		}
		if !strings.Contains(notes, c.notes) {
			t.Errorf("%s does not name %s among the commands that refuse while managedDeployerShipped is false", releaseNotesV090, c.notes)
		}
	}

	// And `fugaro managed flag` is the one managed command the notes say runs:
	// it is not refused by the gate. It stops later — this is a scratch
	// directory with no project config — but never with the shipped gate's
	// message, which is the difference the notes draw.
	out, err := runGated(t, "managed", "flag", "fugaro-newproj-1234", "--budget", "50", "--budget-currency", "USD")
	if err != nil && strings.Contains(err.Error(), managedDisabled) {
		t.Errorf("fugaro managed flag is refused by the shipped gate (%v), and %s says it runs:\n%s", err, releaseNotesV090, out)
	}
	if strings.Contains(out, managedDisabled) {
		t.Errorf("fugaro managed flag printed the shipped gate's refusal, and %s says it runs:\n%s", releaseNotesV090, out)
	}
	if !strings.Contains(notes, "`fugaro managed flag` itself does run") {
		t.Errorf("%s does not say what `fugaro managed flag` does while managedDeployerShipped is false: it runs, and writes marks only the refused apply reads", releaseNotesV090)
	}
}

// commandAt walks the root command's tree for the command a release note names,
// and fails if the tree does not carry it: the words are what a reader types
// after `fugaro`.
func commandAt(t *testing.T, path ...string) *cobra.Command {
	t.Helper()
	cmd := findCommand(path...)
	if cmd == nil {
		t.Fatalf("fugaro %s: no such command (the tree's own definitions are the authority a release note is checked against)", strings.Join(path, " "))
	}
	return cmd
}

// findCommand is commandAt without the failure, for the notes' one claim about
// a command that must not exist.
func findCommand(path ...string) *cobra.Command {
	cmd := NewRootCmd()
	for _, word := range path {
		var next *cobra.Command
		for _, c := range cmd.Commands() {
			if c.Name() == word {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cmd = next
	}
	return cmd
}

// yamlTagOf is a struct field's YAML key, so a note that tells an operator
// which key to write is checked against the struct that parses it rather than
// against the note's own spelling.
func yamlTagOf(t *testing.T, typ reflect.Type, field string) string {
	t.Helper()
	f, ok := typ.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %s", typ, field)
	}
	tag := f.Tag.Get("yaml")
	if tag == "" {
		t.Fatalf("%s.%s has no yaml tag", typ, field)
	}
	return strings.Split(tag, ",")[0]
}

// watchHasBYColumn renders fugaro watch's plain frame and looks for a BY column
// in the runs table's header: the claim is about the frame a reader sees, so it
// is checked against the frame and not against the header's literal.
func watchHasBYColumn(t *testing.T) bool {
	t.Helper()
	var b strings.Builder
	v := watch.View{Mode: "enforce", Day: "2026-10-19", Conn: watch.Connection{Kind: watch.ConnLive},
		Repos: []watch.RepoBlock{{Name: "acme/app", Runs: []watch.RunRow{{Run: "r-1", Title: "work", Stage: "coding", LaunchedBy: "dana"}}}}}
	if err := watch.RenderPlain(&b, "aurora", v, nil, watch.PlainOptions{ASCII: true}); err != nil {
		t.Fatalf("rendering fugaro watch's plain frame: %v", err)
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "RUN") {
			continue
		}
		for _, cell := range releaseNotesCellRE.Split(strings.TrimSpace(line), -1) {
			if strings.TrimSpace(cell) == "BY" {
				return true
			}
		}
	}
	return false
}

// releaseNotesCellRE splits a tabwriter-rendered table line into its cells: the
// columns are separated by two or more spaces.
var releaseNotesCellRE = regexp.MustCompile(`  +`)

// jsonKeysOf is a struct's JSON keys, the wire names a `watch --json` document
// carries: the notes' console bullet names five of them, and the struct is
// where each one is written.
func jsonKeysOf(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		keys[strings.Split(tag, ",")[0]] = true
	}
	// The reader's own guard: every document of both shapes carries these, so a
	// read that finds them is a read of the struct.
	for _, want := range []string{"run", "slug"} {
		if !keys[want] {
			t.Fatalf("%s has no %q key (%d keys): the read is broken, and a struct read wrong is not a fact", typ, want, len(keys))
		}
	}
	return keys
}

// releaseFilesDir is the directory of Highlights files whose format
// docs/releases/README.md states, and which the website's changelog page reads.
const releaseFilesDir = "../../docs/releases"

// publishedReleaseLinks are the markdown links that are already out, by release
// file. A file's release is tagged, the changelog page has rendered the file and
// the GitHub release body has been written from it, so editing the file now
// changes neither: its links are history, not a page that can still be fixed.
// v0.8.6.md's managed-projects limit is the one, and it names the design
// document the pre-0.9.0 way.
//
// The exemption is written out here rather than decided from the checkout's
// tags, which is how it was first and why it was wrong twice over: CI checks the
// repository out with no tags (.github/workflows/ci.yml), so a tag lookup calls
// every file unpublished and the rule fails on v0.8.6.md's link; and once a
// file's release is tagged the same lookup skips every file, so the rule holds
// nothing at all. A release that publishes a file carrying a link adds that link
// here, by hand, at the tag. The entries are checked in both directions below —
// the file has to exist and the link has to still be in it — so an exemption
// that outlives its link fails instead of quietly widening.
var publishedReleaseLinks = map[string][]string{
	"v0.8.6.md": {"[design](../design/managed-projects.md)"},
}

// TestReleaseFilesCarryNoMarkdownLinks holds every release file to the
// renderer's contract: `website/src/components/Changelog.astro`'s inline()
// renders “ `code` “ and `**bold**` and nothing else — "The files carry no link,
// no image and no other construct — the generator hands the lines over as
// written" — so a markdown link in a release file is printed as its own literal
// text on the changelog page, and the repository-relative path it carries
// resolves nowhere in the GitHub release body either. No gate catches it: the
// site's link checker scans built HTML for hrefs (raw markdown produces none)
// and its changelog check asserts only that a rendered line is the file's own
// text verbatim.
//
// The rule is over every release file, not over v0.9.0.md alone, because the
// property is the format's and the file a release is being cut from is exactly
// the one it is for; the files already published are covered too, except for the
// links named in publishedReleaseLinks. `README.md` is skipped by name, the way
// the site's own check skips it: the format document is where the documents a
// release may name are linked.
//
// Mutations (each verified, planted and reverted): put
// `[docs/project-layer.md](../project-layer.md)` back into the 0.9.0 notes'
// known limits — the link this test was written for — and it fails on v0.9.0.md;
// delete the excused link from v0.8.6.md and it fails on the stale exemption.
func TestReleaseFilesCarryNoMarkdownLinks(t *testing.T) {
	entries, err := os.ReadDir(releaseFilesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", releaseFilesDir, err)
	}
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		files++
		data, err := os.ReadFile(releaseFilesDir + "/" + name)
		if err != nil {
			t.Fatalf("reading %s/%s: %v", releaseFilesDir, name, err)
		}
		excused := publishedReleaseLinks[name]
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "](") && !strings.Contains(line, "![") {
				continue
			}
			if releaseLinkIsExcused(excused, line) {
				continue
			}
			t.Errorf("%s/%s:%d carries a markdown link or image (%q): the changelog page renders a release file's lines as they are written — `code` and **bold** only, per website/src/components/Changelog.astro's inline() — so a link is printed as its literal text there and resolves nowhere in the release body; name the document in prose or in a `code` span instead",
				releaseFilesDir, name, i+1, strings.TrimSpace(line))
		}
	}
	if files == 0 {
		t.Errorf("%s holds no release file: the enumeration is broken, and a rule over no files is not a rule", releaseFilesDir)
	}
	// The other direction: an exemption for a file that is gone, or for a link
	// that has been fixed, is an exemption nobody decided to keep.
	for name, links := range publishedReleaseLinks {
		data, err := os.ReadFile(releaseFilesDir + "/" + name)
		if err != nil {
			t.Errorf("publishedReleaseLinks excuses links in %s, which %s does not hold: %v", name, releaseFilesDir, err)
			continue
		}
		for _, link := range links {
			if !strings.Contains(string(data), link) {
				t.Errorf("publishedReleaseLinks excuses %q in %s, which no longer carries it: drop the exemption — an unlinked release file needs none", link, name)
			}
		}
	}
}

// releaseLinkIsExcused reports whether a line carrying a link is one of the
// links a published release file is allowed to keep.
func releaseLinkIsExcused(excused []string, line string) bool {
	for _, link := range excused {
		if strings.Contains(line, link) {
			return true
		}
	}
	return false
}
