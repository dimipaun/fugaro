package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// allowDeleteRig is the installation's own runs bucket, seeded as the read
// proves it: the fakes' project number, and the label the installation puts
// on it. The writer proves both before it writes, so a rig that wants its
// list recorded has to seed both.
func allowDeleteRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	r := newInitRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	return r
}

// allowDeleteAt is the delete list's bytes in bucket, and whether it is there.
func allowDeleteAt(t *testing.T, g *gcpfake.GCS, bucket string) (string, bool) {
	t.Helper()
	b, err := g.Bucket(t, bucket).Bucket.ReadAll(context.Background(), managed.AllowDeleteObject)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// approval is one approval fixture in this file: the Terraform address the
// owner approves, and the id of the object the plan shows there — the part of
// the identity that tells one object at an address from another one.
type approval struct{ address, id string }

// arg is the approval as this command's own argument, ADDRESS=IDENTITY, which
// is what the owner pastes out of a refused apply.
func (a approval) arg() string {
	return a.address + "=type=google_storage_bucket id=" + a.id
}

// allowDeleteJSON is the object the command writes for these approvals:
// version 2, the project, and an entry per approval, sorted and deduplicated
// the way the command sorts them.
func allowDeleteJSON(project string, ap ...approval) string {
	seen := map[string]bool{}
	var entries []string
	for _, a := range ap {
		if seen[a.arg()] {
			continue
		}
		seen[a.arg()] = true
		entries = append(entries, `{"address":`+jsonString(a.address)+`,"before":`+jsonString("type=google_storage_bucket id="+a.id)+`}`)
	}
	slices.Sort(entries)
	return `{"version":2,"gcp_project":` + jsonString(project) + `,"entries":[` + strings.Join(entries, ",") + `]}`
}

// allowDeleteArgs is the command line for these approvals: the command's own
// name, then one ADDRESS=IDENTITY argument per approval.
func allowDeleteArgs(ap ...approval) []string {
	out := []string{"allow-delete"}
	for _, a := range ap {
		out = append(out, a.arg())
	}
	return out
}

// the two approvals most tests here use, and the object they write.
var (
	dataApproval = approval{"google_storage_bucket.data", "data-bucket"}
	webApproval  = approval{"google_cloud_run_v2_job.web", "web-job"}
)

// TestAllowDeleteCommandAsksBeforeItDropsAnApproval: the write replaces the
// list, so an invocation that leaves an address out removes an approval the
// owner gave — and a silently dropped approval is the fail-closed direction
// only by accident: the owner believes it is still there. The command names
// the entries it is about to remove and asks for the project's name before it
// writes a list that drops any of them.
func TestAllowDeleteCommandAsksBeforeItDropsAnApproval(t *testing.T) {
	r := allowDeleteRig(t)
	if _, _, err := execute(t, allowDeleteArgs(dataApproval, webApproval)...); err != nil {
		t.Fatal(err)
	}
	before, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the list was not written")
	}
	// No terminal: the removal is refused, and nothing is written.
	out, _, err := execute(t, allowDeleteArgs(webApproval)...)
	if err == nil {
		t.Fatalf("the removal ran without a confirmation:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(out, dataApproval.arg()) {
		t.Errorf("the command does not say which approval it would drop:\n%s", out)
	}
	// And the refusal names no flag this command lacks. It used to end "or
	// pass --yes once you have read what it does", which fugaro allow-delete
	// does not define (it registers the cloud flags and nothing else), so the
	// owner was told to pass a flag that does not exist. Every --token the
	// refusal prints is now checked against the command's own flags.
	assertNamesNoFlagItLacks(t, newAllowDeleteCmd(), err)
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a refused removal changed the list: %s → %s", before, after)
	}
	// A terminal, but the wrong name typed: still nothing written.
	terminal(t, true)
	out, _, err = executeStdin(t, "not-the-name\n", allowDeleteArgs(webApproval)...)
	if err == nil {
		t.Fatalf("a mistyped confirmation wrote the list:\n%s", out)
	}
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a mistyped confirmation changed the list: %s → %s", before, after)
	}
	// The project's name typed: the shorter list is written.
	out, _, err = executeStdin(t, initProjectName+"\n", allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the confirmed removal wrote nothing")
	}
	want := allowDeleteJSON("proj-1234", webApproval)
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// And a write that drops nothing asks nothing: the same list again is not
	// a removal.
	if _, _, err := execute(t, allowDeleteArgs(webApproval)...); err != nil {
		t.Errorf("re-approving what is already approved asked for a confirmation: %v", err)
	}
}

// TestAllowDeleteCommandAsksBeforeItReplacesAnUnreadableObject: an object at
// the list's name that is not a list this command reads (hand-edited, from a
// future version, or about another installation) held approvals nobody can
// read, so replacing it is confirmed the way a removal is — and the question
// tells the owner what it is: an unreadable object replaced, not "removing 0
// address(es)", which said nothing was being lost.
func TestAllowDeleteCommandAsksBeforeItReplacesAnUnreadableObject(t *testing.T) {
	r := allowDeleteRig(t)
	const unreadable = `{"version":99,"gcp_project":"proj-1234","entries":[{"address":"google_storage_bucket.data","before":"type=google_storage_bucket id=data-bucket"}]}`
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, []byte(unreadable))
	// No terminal: refused, nothing written.
	out, _, err := execute(t, allowDeleteArgs(webApproval)...)
	if err == nil {
		t.Fatalf("an unreadable list was replaced without a confirmation:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if got, _ := allowDeleteAt(t, r.gcs, initRunsBucket); got != unreadable {
		t.Errorf("a refused replacement changed the object: %s", got)
	}
	if strings.Contains(out, "removing 0") {
		t.Errorf("the confirmation says nothing is removed while an unreadable object is replaced:\n%s", out)
	}
	if !strings.Contains(out, "nothing it held could be read") {
		t.Errorf("the confirmation does not say what it held could not be read:\n%s", out)
	}
	// The project's name typed: the object is replaced by the list.
	terminal(t, true)
	out, _, err = executeStdin(t, initProjectName+"\n", allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := allowDeleteJSON("proj-1234", webApproval)
	if got, _ := allowDeleteAt(t, r.gcs, initRunsBucket); got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
}

// assertNamesNoFlagItLacks: a refusal that tells the owner to pass a flag the
// command does not define is advice that cannot be taken — fugaro allow-delete
// registers the cloud flags and nothing else, so "or pass --yes" named a flag
// that does not exist. Every --token the message prints is looked up in the
// command's own flags, the way the docs tests look a --flag up in a command
// before believing it.
func assertNamesNoFlagItLacks(t *testing.T, cmd *cobra.Command, err error) {
	t.Helper()
	if cmd.Flags().Lookup("yes") != nil {
		t.Fatalf("fugaro allow-delete defines --yes now: this assertion is about a command that has none")
	}
	for _, f := range strings.Fields(err.Error()) {
		name := docFlagToken(f)
		if name == "" {
			continue
		}
		if cmd.Flags().Lookup(name) == nil && cmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("the refusal names --%s, which this command does not define: %v", name, err)
		}
	}
}

// terminal patches the terminal test budget's way: the command asks for a
// typed confirmation only when stdin is one.
func terminal(t *testing.T, is bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return is }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// TestAllowDeleteCommandWritesTheWholeList is the writer's contract: the
// owner's approval, recorded at the runs bucket's root, whole on every
// invocation — a shorter list refuses more, and nothing an owner took off the
// list stays deletable.
func TestAllowDeleteCommandWritesTheWholeList(t *testing.T) {
	r := allowDeleteRig(t)
	out, _, err := execute(t, allowDeleteArgs(dataApproval, webApproval, webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Sorted and deduplicated, so one list has one encoding: the object is
	// compared by eye and by generation.
	want := allowDeleteJSON("proj-1234", dataApproval, webApproval)
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("no %s in gs://%s after the command:\n%s", managed.AllowDeleteObject, initRunsBucket, out)
	}
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// A second invocation with one address replaces the list rather than
	// merging into it.
	// The second invocation drops google_storage_bucket.data, so it is a
	// removal: it is confirmed the way any removal is (TestAllowDeleteCommand-
	// AsksBeforeItDropsAnApproval is where that rule is asserted).
	terminal(t, true)
	out, _, err = executeStdin(t, initProjectName+"\n", allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok = allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the second command removed the list:\n%s", out)
	}
	if want := allowDeleteJSON("proj-1234", webApproval); got != want {
		t.Errorf("after the second command gs://%s/%s = %s, want %s (the whole list, not an append)", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
}

// TestAllowDeleteCommandPrintsTheListAndWritesNothing: with no addresses the
// command prints what is approved. It never empties the list — a write that
// emptied it would refuse every delete under the appearance of a change, and
// the owner's way to refuse one is to write the list without it.
func TestAllowDeleteCommandPrintsTheListAndWritesNothing(t *testing.T) {
	r := allowDeleteRig(t)
	// No list yet: the print says so rather than writing an empty one.
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "allows no entry") {
		t.Errorf("with no list the command printed:\n%s", out)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("printing the list wrote one: gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	if _, _, err := execute(t, allowDeleteArgs(dataApproval, webApproval)...); err != nil {
		t.Fatal(err)
	}
	before, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the list was not written")
	}
	out, _, err = execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Each line is the argument form, so what the print shows can be pasted
	// back: the address and the identity it is approved for.
	for _, a := range []approval{dataApproval, webApproval} {
		if !strings.Contains(out, a.arg()) {
			t.Errorf("the print does not name %s:\n%s", a.arg(), out)
		}
	}
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("printing the list changed it: %s → %s", before, after)
	}
}

// TestAllowDeleteCommandRefusesUnderACodingAgentMarker: the command records
// the owner's approval of deletes in the cloud with the owner's own
// credentials, and a coding agent's session holds those credentials and could
// pass the flag itself — so nothing is written from one, whatever the
// arguments.
func TestAllowDeleteCommandRefusesUnderACodingAgentMarker(t *testing.T) {
	r := allowDeleteRig(t)
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, allowDeleteArgs(dataApproval)...)
	if err == nil {
		t.Fatalf("the command ran in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// Printing the list is a read, but it is reached by the same command, so
	// it is refused from an agent's session too: the command's whole surface
	// is the owner's, and a semi-idempotent half of it is a half that can be
	// mistaken for the approval path.
	if _, _, err := execute(t, "allow-delete"); err == nil {
		t.Error("printing the list is allowed in a coding agent's session")
	}
}

// TestAllowDeleteCommandRefusesAnArgumentThatIsNotAnEntry: both halves of an
// entry are compared with a plan's by exact equality, so a stray space around
// either would be an approval of nothing at all — the command trims what it is
// given — and an argument that does not carry both halves is refused with the
// message for it rather than the one about the bucket the writer would
// otherwise fail on.
//
// The identity half is required to begin with the resource type, which is what
// a refused apply prints (tf.RemoveIdentityText). That is also what makes the
// form's one ambiguity fail closed rather than silently: an address whose own
// text contains an "=" (an index key, as in google_x.y["a=b"]) is cut inside
// the address, so the half after the cut does not begin with "type=" and the
// argument is refused rather than recorded as an approval that matches
// nothing.
func TestAllowDeleteCommandRefusesAnArgumentThatIsNotAnEntry(t *testing.T) {
	r := allowDeleteRig(t)
	for name, arg := range map[string]string{
		"nothing at all":    "",
		"spaces":            "   ",
		"a tab":             "\t",
		"an address alone":  "google_storage_bucket.data",
		"an identity alone": "=type=google_storage_bucket id=data-bucket",
		"not an identity":   "google_storage_bucket.data=id=data-bucket",
		"a bare word":       "google_storage_bucket.data=yes",
		// The address holds an "=", so the cut lands inside it: what the cut
		// leaves is an address still inside its index key where the identity
		// begins, and the command refuses rather than recording an entry about
		// a truncated address that no plan has.
		"an address holding an equals sign": `google_storage_bucket.data["a=b"]=type=google_storage_bucket id=x`,
		"a trailing equals sign":            "google_storage_bucket.data=",
	} {
		t.Run(name, func(t *testing.T) {
			out, _, err := execute(t, "allow-delete", arg)
			if err == nil {
				t.Fatalf("the command accepted %q:\n%s", arg, out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("%q: exit %d, want %d", arg, ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "ADDRESS=IDENTITY") && !strings.Contains(err.Error(), "an identity begins with the resource type") && !strings.Contains(err.Error(), "inside an index key") {
				t.Errorf("%q was refused with the wrong reason: %v", arg, err)
			}
			if strings.Contains(err.Error(), "runs_bucket") {
				t.Errorf("%q was reported as a bucket problem: %v", arg, err)
			}
		})
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// And what it does write is trimmed on both sides of the cut, so the
	// object holds halves that can match a plan's.
	trimmed := "  google_storage_bucket.data = type=google_storage_bucket id=data-bucket  "
	if _, _, err := execute(t, "allow-delete", trimmed); err != nil {
		t.Fatal(err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the trimmed argument was not written")
	}
	want := allowDeleteJSON("proj-1234", dataApproval)
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// And the command is idempotent on the object it just wrote: the same
	// approvals again are the same bytes, which is what makes the print
	// paste-backable.
	if _, _, err := execute(t, allowDeleteArgs(dataApproval)...); err != nil {
		t.Fatal(err)
	}
	if again, _ := allowDeleteAt(t, r.gcs, initRunsBucket); again != want {
		t.Errorf("the same approvals wrote %s, want %s", again, want)
	}
}

// TestAllowDeleteCommandAcceptsAQuotedIdentity: the loop the help text
// describes is a person copying the identity out of a refusal and pasting it
// back, and a refusal is printed bare (tf.RemoveIdentityText) so that the paste
// is the identity itself. A person may still quote what they paste — a shell
// of their own, a message someone else quoted — so one surrounding pair of
// double quotes is stripped, which is the only mark removed: the text inside
// them must begin with "type=" like any other argument, so the fail-closed
// direction is unchanged (the row above shows a quoted non-identity refused).
func TestAllowDeleteCommandAcceptsAQuotedIdentity(t *testing.T) {
	r := allowDeleteRig(t)
	quoted := dataApproval.address + `="type=google_storage_bucket id=data-bucket"`
	if _, _, err := execute(t, "allow-delete", quoted); err != nil {
		t.Fatalf("a quoted identity was refused: %v", err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("nothing was written")
	}
	// The object holds the identity without the quotes: what was stripped is
	// not recorded, and the stored text is the one a plan's before view
	// produces.
	if want := allowDeleteJSON("proj-1234", dataApproval); got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// And the printed list is pasteable, which is the other end of the same
	// loop: the refusal's identity and the listing's line are one text.
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, dataApproval.arg()) || strings.Contains(out, `"`) {
		t.Errorf("the listing is not the pasteable form:\n%s", out)
	}
}

// TestAllowDeleteCommandKeepsAOneSidedQuote: what is stripped is one MATCHED
// pair, because the pair is the mark a shell or a message adds around a value
// this command printed bare. A quote at one end only is not that mark: an
// identity is compared byte for byte and a value in one may hold a quote, so
// the ends are not stripped independently. Stripping them was the earlier
// behaviour and it approved an object nobody named — with a trailing quote it
// recorded the identity without the quote, an entry that matches no removal and
// so clears nothing while the owner believes the address is clear.
//
// A leading quote alone is refused rather than completed into an identity: the
// text after the cut must begin with "type=" like any other argument, and
// closing that quote for the owner would be guessing at which identity they
// meant.
func TestAllowDeleteCommandKeepsAOneSidedQuote(t *testing.T) {
	r := allowDeleteRig(t)
	arg := dataApproval.address + `=type=google_storage_bucket id=data-bucket"`
	if _, _, err := execute(t, "allow-delete", arg); err != nil {
		t.Fatalf("a trailing quote was refused: %v", err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("nothing was written")
	}
	// The quote is a character of the identity and is recorded as itself: the
	// bytes an entry holds are the bytes the plan would show.
	want := `{"version":2,"gcp_project":"proj-1234","entries":[{"address":` +
		jsonString(dataApproval.address) + `,"before":` +
		jsonString(`type=google_storage_bucket id=data-bucket"`) + `}]}`
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// And an argument whose only quote is the leading one is not an identity
	// at all, so it is refused and nothing is written for it.
	r2 := allowDeleteRig(t)
	lead := dataApproval.address + `="type=google_storage_bucket id=data-bucket`
	out, _, err := execute(t, "allow-delete", lead)
	if err == nil {
		t.Fatalf("an unpaired leading quote was accepted:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("%q: exit %d, want %d", lead, ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "an identity begins with the resource type") {
		t.Errorf("%q was refused with the wrong reason: %v", lead, err)
	}
	if got, ok := allowDeleteAt(t, r2.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
}

// TestAllowDeleteCommandNeedsTheDefaultRunsBucket: the managed objects are
// only ever read from the project's runs bucket under its default name — the
// rule init's owner() and markCreated already apply — so a list recorded in a
// custom bucket is one nothing looks in. The command refuses rather than
// recording an approval where the read will never find it.
func TestAllowDeleteCommandNeedsTheDefaultRunsBucket(t *testing.T) {
	r := allowDeleteRig(t)
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(cfg), "runs_bucket: "+initRunsBucket, "runs_bucket: aurora-own-runs", 1)
	if custom == string(cfg) {
		t.Fatal("the rig's config does not name the default runs bucket")
	}
	if err := os.WriteFile(r.cfg, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{allowDeleteArgs(dataApproval), {"allow-delete"}} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v ran against a custom runs bucket:\n%s", args, out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), initRunsBucket) {
			t.Errorf("%v: the refusal does not name the name it needs: %v", args, err)
		}
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
}

// TestAllowDeleteCommandRefusesABucketThatIsNotTheProjectsOwn: a bucket name
// is global, so a list written into a stranger's bucket of the same name
// would be one nothing reads and the owner's approval would be lost. The
// command says which bucket it needs rather than writing into it.
func TestAllowDeleteCommandRefusesABucketThatIsNotTheProjectsOwn(t *testing.T) {
	// The bucket the rig seeds is the project's own; each row replaces it with
	// one that is not, so the assertion is about the proof rather than about a
	// bucket that is not there at all (internal/infra/managed's own test
	// covers the absent bucket, which no CLI rig can seed away).
	for name, seed := range map[string]func(*testing.T, *initRig){
		"no label": func(t *testing.T, r *initRig) { r.gcs.AddBucket(initRunsBucket, initProjectNumber, nil) },
		"another project number": func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber+1, managedBucketLabels)
		},
		"a mark of someone else": func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed-by-someone"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := allowDeleteRig(t)
			seed(t, r)
			out, _, err := execute(t, allowDeleteArgs(dataApproval)...)
			if err == nil {
				t.Fatalf("the command wrote gs://%s/%s:\n%s", initRunsBucket, managed.AllowDeleteObject, out)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
			if !strings.Contains(err.Error(), initRunsBucket) || !strings.Contains(err.Error(), initProjectName) {
				t.Errorf("the refusal names neither the bucket nor the project: %v", err)
			}
		})
	}
}

// allowDeleteGeneration is the list's generation, 0 when it is absent, read
// from a listing so that a test that fails the object's reads can still see
// whether it was rewritten.
func allowDeleteGeneration(t *testing.T, g *gcpfake.GCS, bucket string) int64 {
	t.Helper()
	it := g.Client(t).Bucket(bucket).Objects(context.Background(), &storage.Query{Prefix: managed.AllowDeleteObject})
	attrs, err := it.Next()
	if err != nil {
		return 0
	}
	return attrs.Generation
}

// TestAllowDeleteCommandWritesNothingWhenItCannotReadTheList: the write
// replaces the list and asks before it drops an approval, so a read that
// failed must not be taken for "nothing approved" — that would drop every
// approval without the question. Nothing is written, and the error says why.
func TestAllowDeleteCommandWritesNothingWhenItCannotReadTheList(t *testing.T) {
	r := allowDeleteRig(t)
	if _, _, err := execute(t, allowDeleteArgs(dataApproval, webApproval)...); err != nil {
		t.Fatal(err)
	}
	before := allowDeleteGeneration(t, r.gcs, initRunsBucket)
	if before == 0 {
		t.Fatal("the list was not written")
	}
	r.gcs.FailObjectGetsAfter(initRunsBucket, managed.AllowDeleteObject, 0)
	out, _, err := execute(t, allowDeleteArgs(approval{"google_compute_network.vpc", "vpc-network"})...)
	if err == nil {
		t.Fatalf("a write after a failed read ran:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say nothing was written: %v", err)
	}
	// The read's own refusal, not the write's generation precondition: a
	// command that wrote anyway after the failed read would be stopped by
	// that precondition here only because the list already exists.
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("the refusal is not the failed read's: %v", err)
	}
	if after := allowDeleteGeneration(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a write after a failed read rewrote the list (generation %d → %d)", before, after)
	}
}

// TestAllowDeleteCommandRefusesAListThatChangedUnderIt: the error a
// concurrent write produces is the owner's to act on, and says what to do.
func TestAllowDeleteCommandRefusesAListThatChangedUnderIt(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: "proj-1234"}
	err := allowDeleteWriteErr(lc, initRunsBucket, fmt.Errorf("%w: generation 7", managed.ErrChanged))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "run the command again") {
		t.Errorf("allowDeleteWriteErr(ErrChanged) = %v (exit %d), want a user error that says to run it again", err, ExitCode(err))
	}
}

// TestAllowDeleteCommandConditionsItsWriteOnWhatItRead: the command reads the
// list, asks, and writes conditioned on the generation it read, so a writer
// that replaces the object while the owner is at the prompt is not
// overwritten — and its approvals are not dropped by a write that never saw
// them. This is the compare-and-swap end to end: the test holds the command at
// its confirmation, replaces the object behind it, and lets it proceed. The
// write that follows must be refused (ErrChanged), and the object must still
// be the other writer's.
//
// Before this test the ErrChanged path was reached only through
// allowDeleteWriteErr, as a string formatter: nothing ran the command against
// a list that changed under it. What pins the conditioning itself is the
// second write of TestAllowDeleteCommandWritesTheWholeList — an unconditioned
// create of an object that exists is refused by GCS and by the fake alike, so
// a write that dropped the generation fails there.
func TestAllowDeleteCommandConditionsItsWriteOnWhatItRead(t *testing.T) {
	r := allowDeleteRig(t)
	if _, _, err := execute(t, allowDeleteArgs(dataApproval, webApproval)...); err != nil {
		t.Fatal(err)
	}
	terminal(t, true)
	pr, pw := io.Pipe()
	out := newPromptWriter()
	cmd := NewRootCmd()
	var errOut bytes.Buffer
	cmd.SetIn(pr)
	cmd.SetOut(out)
	cmd.SetErr(&errOut)
	// One address of the two: a removal, which is the branch that asks.
	cmd.SetArgs(allowDeleteArgs(webApproval))
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	select {
	case <-out.asked():
	case <-time.After(30 * time.Second):
		t.Fatalf("the command never asked for the confirmation:\n%s", out.String())
	}
	// While it waits for the name: another writer replaces the list.
	other := []byte(allowDeleteJSON("proj-1234", approval{"google_compute_network.vpc", "vpc-network"}))
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, other)
	if _, err := pw.Write([]byte(initProjectName + "\n")); err != nil {
		t.Fatal(err)
	}
	err := <-done
	if err == nil {
		t.Fatalf("a list written over another writer's list was not refused:\n%s", out.String())
	}
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "run the command again") {
		t.Errorf("the refusal = %v (exit %d), want a user error that says to run the command again", err, ExitCode(err))
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); !ok || got != string(other) {
		t.Errorf("gs://%s/%s = %q (there: %v), want the other writer's list, unchanged", initRunsBucket, managed.AllowDeleteObject, got, ok)
	}
}

// promptWriter collects a command's output and lets a test act at the moment
// the command is waiting for the owner: the write's own buffer is read from
// the test's goroutine, so it is guarded, and the signal is sent once, when
// the confirmation prompt appears.
type promptWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	signal chan struct{}
	once   sync.Once
}

func newPromptWriter() *promptWriter { return &promptWriter{signal: make(chan struct{})} }

func (w *promptWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "Type ") {
		w.once.Do(func() { close(w.signal) })
	}
	return n, err
}

func (w *promptWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// asked is the signal that the confirmation prompt is on the page: the
// command is blocked reading stdin, and the test owns the pipe.
func (w *promptWriter) asked() <-chan struct{} { return w.signal }

// TestAllowDeleteCommandAsksBeforeItReplacesAnObjectItCannotRead: an object
// that is there but is not a list this command reads is replaced only on the
// typed confirmation, because what it holds is unknown — the command cannot
// say which approvals a write would drop. It says so, asks, and writes only
// when the project's name is typed.
func TestAllowDeleteCommandAsksBeforeItReplacesAnObjectItCannotRead(t *testing.T) {
	r := allowDeleteRig(t)
	// A body AllowDelete would not read: the object is there, and it holds
	// no addresses this command could report.
	foreign := []byte(`{"version":2,"gcp_project":"proj-1234"}`)
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, foreign)
	// No terminal: nothing replaces an object whose contents are unknown.
	out, _, err := execute(t, allowDeleteArgs(dataApproval)...)
	if err == nil {
		t.Fatalf("an unreadable list was replaced without a confirmation:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(out, "is not a list this command reads") {
		t.Errorf("the command does not say what it would replace:\n%s", out)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); !ok || got != string(foreign) {
		t.Errorf("a refused run wrote gs://%s/%s = %q", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// A terminal, but the wrong name: still nothing.
	terminal(t, true)
	if _, _, err := executeStdin(t, "not-the-name\n", allowDeleteArgs(dataApproval)...); err == nil {
		t.Error("a mistyped confirmation replaced an unreadable list")
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); !ok || got != string(foreign) {
		t.Errorf("a mistyped confirmation wrote gs://%s/%s = %q", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// The name typed: the object is replaced by the list, and the command
	// says what it holds now.
	out, _, err = executeStdin(t, initProjectName+"\n", allowDeleteArgs(dataApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the confirmed replacement wrote nothing")
	}
	want := allowDeleteJSON("proj-1234", dataApproval)
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
}

// TestAllowDeleteCommandRefusesAnOverSizeList: a list past the size the read
// believes would be written and then read back as no addresses, silently
// refusing every delete it looks like it approves. The command refuses, and
// its message is about the list — not about the bucket, whose two settings
// are fine.
func TestAllowDeleteCommandRefusesAnOverSizeList(t *testing.T) {
	r := allowDeleteRig(t)
	entries := make([]approval, 0, 2500)
	for i := range cap(entries) {
		entries = append(entries, approval{fmt.Sprintf("google_cloud_run_v2_job.web_%04d", i), fmt.Sprintf("job-%04d", i)})
	}
	out, _, err := execute(t, allowDeleteArgs(entries...)...)
	if err == nil {
		t.Fatalf("a list of %d entries was written:\n%s", len(entries), out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "fewer addresses") || !strings.Contains(err.Error(), "over the size") {
		t.Errorf("the refusal does not say the list is what to fix: %v", err)
	}
	if strings.Contains(err.Error(), "runs_bucket") || strings.Contains(err.Error(), "gcp_project") {
		t.Errorf("the refusal sends the owner to settings that are fine: %v", err)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
}

// hostileEntries are entries an object in the bucket can carry and
// parseDeleteList accepts (it refuses only blank ones): a C1 CSI (U+009B), a
// bidi override (U+202E), a newline, and a 10,000-character string — in both
// halves of the entry, the address and the identity, because the listing
// prints both.
var hostileEntries = []approval{
	{"csi\u009b31m.x", "csi\u009b31m"},
	{"bidi‮evil.x", "bidi‮evil"},
	{"line\nforged.x", "line\nforged"},
	{strings.Repeat("z", 10000), strings.Repeat("z", 10000)},
}

// assertNoRawHostile fails when out carries any hostile entry as itself: the
// C1 and bidi characters, the newline inside an entry, or a line longer than
// one safetext.Field value can be.
func assertNoRawHostile(t *testing.T, out string) {
	t.Helper()
	if strings.ContainsAny(out, "\u009b‮") {
		t.Errorf("the output carries a raw control or bidi character: %q", out)
	}
	if strings.Contains(out, "line\nforged.x") {
		t.Error("the output carries an entry's newline as a line break")
	}
	for _, l := range strings.Split(out, "\n") {
		if len([]rune(l)) > 1000 {
			t.Errorf("an output line is %d runes long: an entry was printed uncut", len([]rune(l)))
		}
	}
}

// M4: the addresses printed from the bucket go to stdout, not through
// ErrorText, so each is safetext.Field'd at the print: the listing and the
// "this list leaves out" lines.
func TestAllowDeletePrintsBucketAddressesSanitised(t *testing.T) {
	list := allowDeleteJSON(initProject, hostileEntries...)
	t.Run("the listing", func(t *testing.T) {
		r := allowDeleteRig(t)
		r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, []byte(list))
		out, _, err := execute(t, "allow-delete")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, "approves 4 entry(ies)") {
			t.Fatalf("the hostile list was not read as one, so the print is not exercised:\n%q", out)
		}
		assertNoRawHostile(t, out)
	})
	t.Run("the entries a write would drop", func(t *testing.T) {
		r := allowDeleteRig(t)
		r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, []byte(list))
		out, _, _ := execute(t, allowDeleteArgs(dataApproval)...)
		if !strings.Contains(out, "leaves out 4") {
			t.Fatalf("the drop listing was not printed:\n%q", out)
		}
		assertNoRawHostile(t, out)
	})
}

// Round 3, L2: every user-facing branch of allowDeleteWriteErr interpolates
// the cause through safetext.Field.
func TestAllowDeleteWriteErrSanitisesTheCause(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	payload := "csi\u009b31m bidi‮ line\nforged " + strings.Repeat("z", 5000)
	for name, err := range map[string]error{
		"too large":     fmt.Errorf("%w: %s", managed.ErrTooLarge, payload),
		"refused":       fmt.Errorf("%w: %s", managed.ErrRefused, payload),
		"changed":       fmt.Errorf("%w: %s", managed.ErrChanged, payload),
		"access denied": &googleapi.Error{Code: http.StatusForbidden, Message: payload},
	} {
		got := allowDeleteWriteErr(lc, initRunsBucket, err).Error()
		if strings.ContainsAny(got, "\u009b‮\n") || len([]rune(got)) > 1500 {
			t.Errorf("%s: the refusal carries the raw cause: %q", name, got[:300])
		}
	}
}

// TestAllowDeleteCommandListsALongIdentityWhole: the listing is the second
// place the owner pastes a line from, and the line has to be the entry. An
// identity is longer than safetext.Field's own bound as soon as an object's
// name is (a bucket object's id is its bucket and its name, and a path-shaped
// name of a few hundred characters is ordinary), and a line cut at that bound
// would look like an entry and approve nothing — so the listing prints the
// line itself, and the count bound is the only bound it keeps.
func TestAllowDeleteCommandListsALongIdentityWhole(t *testing.T) {
	r := allowDeleteRig(t)
	name := strings.Repeat("x", 600)
	identity := "type=google_storage_bucket project=proj-1234 name=" + name + " id=" + name + " instance=2026-01-02T03:04:05.678Z"
	if len(identity) <= safetext.FieldMax {
		t.Fatalf("the fixture identity is %d characters, too short to be cut by safetext.Field (%d)", len(identity), safetext.FieldMax)
	}
	arg := dataApproval.address + "=" + identity
	if _, _, err := execute(t, "allow-delete", arg); err != nil {
		t.Fatalf("a long identity was refused: %v", err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("nothing was written")
	}
	if !strings.Contains(got, identity) {
		t.Errorf("the object does not hold the identity as it was given:\n%s", got)
	}
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, arg) {
		t.Errorf("the listing does not print the line it can be pasted from:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("the listing cut a line, so a paste from it would approve nothing:\n%s", out)
	}
}

// refusalRemoval is the removal a refusal is about, built as the plan's own
// JSON shows it (a delete's before view), so the identity is read off it the
// way the gate reads one rather than spelled out beside it.
func refusalRemoval(name string) tf.ResourceChange {
	return tf.ResourceChange{
		Address: "module.managed.google_storage_bucket.fmgd_data",
		Type:    "google_storage_bucket",
		Change: tf.Change{Actions: []string{"delete"}, Before: map[string]any{
			"id": name, "name": name, "project": initProject, "time_created": "2026-01-02T03:04:05.678Z"}},
	}
}

// TestAllowDeleteCommandRecordsTheIdentityARefusalPrints: the loop the delete
// list works by is textual — a refusal prints the identity of the object the
// plan removes, the owner pastes that text into this command, and the entry it
// records is compared with the plan's identity byte for byte (tf.Approves) —
// so both ends of it are walked here on the values whose escaping is doing
// work: a value with a space in it, a value that ENDS in one (the escaped
// space and the space between two parts would be a run of whitespace, which
// every printer of this program collapses with strings.Fields, and which this
// command's own TrimSpace takes off the end of an argument), a value that is
// only spaces, and an ordinary one.
func TestAllowDeleteCommandRecordsTheIdentityARefusalPrints(t *testing.T) {
	for _, name := range []string{"fmgd-data", "fmgd data", "fmgd-data ", "  "} {
		t.Run(fmt.Sprintf("a name %q", name), func(t *testing.T) {
			rc := refusalRemoval(name)
			// What a refusal prints is what the gate compares, before anything
			// is pasted.
			if printed, want := tf.RemoveIdentityText(rc), tf.RemovedIdentity(rc).String(); printed != want {
				t.Fatalf("a refusal prints %q and the gate compares %q", printed, want)
			}
			r := allowDeleteRig(t)
			// The paste: the line a refusal printed, with the whitespace a
			// terminal or a message adds around it.
			arg := "  " + rc.Address + "=" + tf.RemoveIdentityText(rc) + "  "
			if out, _, err := execute(t, "allow-delete", arg); err != nil {
				t.Fatalf("the identity a refusal printed was refused: %v\n%s", err, out)
			}
			got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
			if !ok {
				t.Fatal("nothing was written")
			}
			var obj struct {
				Version int              `json:"version"`
				Entries []tf.DeleteEntry `json:"entries"`
			}
			if err := json.Unmarshal([]byte(got), &obj); err != nil {
				t.Fatalf("the object is not a delete list (%v):\n%s", err, got)
			}
			want := tf.ApprovalFor(rc)
			if len(obj.Entries) != 1 || obj.Entries[0] != want {
				t.Errorf("the object holds %+v, want the one entry that approves the removal (%+v)", obj.Entries, want)
			}
			if ok, why := tf.Approves(obj.Entries, rc); !ok {
				t.Errorf("the entry recorded from the refusal does not approve the removal: %s", why)
			}
		})
	}
}

// TestAllowDeleteCommandTakesTheSentenceBracketOffAPaste: what a refusal
// prints is a sentence of this program's own with the identity inside it — "…
// (the plan removes type=… id=…)" — so an owner who copies the printed text
// out, which is what this command's help tells them to do, arrives at the
// command with the sentence's closing bracket still on the end. It is taken
// off, and only one, and the entry recorded is the one that approves the
// removal.
//
// The other direction is pinned too: an argument that still holds a parenthesis
// after that is refused, loudly, rather than recorded. An identity holds none
// (tf.identityValue escapes both), so it is not an identity, and recording it
// would be an entry the owner reads as an approval and that clears nothing —
// the loop that cannot close.
func TestAllowDeleteCommandTakesTheSentenceBracketOffAPaste(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	rc := refusalRemoval("fmgd-data")
	// The sentence a refusal prints, and the text its reader copies out of it:
	// everything after the sentence's own words (tf.Approves and the cover say
	// the same thing in the same shape, internal/infra/tf/managed_cover.go).
	_, why := tf.Approves([]tf.DeleteEntry{{Address: addr, Before: "type=google_storage_bucket id=another-object"}}, rc)
	const mark = "and the plan removes "
	i := strings.LastIndex(why, mark)
	if i < 0 {
		t.Fatalf("the refusal does not name the object the plan removes: %q", why)
	}
	tail := why[i+len(mark):]
	if want := tf.RemoveIdentityText(rc) + ")"; tail != want {
		t.Fatalf("the refusal's copy is %q, want the identity followed by the sentence's own bracket (%q)", tail, want)
	}

	for _, tc := range []struct {
		name, before string
	}{
		{"the copied tail, bracket and all", tail},
		{"the copied tail, quoted and bracketed", `"` + tail + `"`},
		{"the copied tail with a space after it", tail + " "},
		// A space BEFORE the bracket: a paste that copied the words loosely,
		// or a refusal whose line wrapped, puts one between the identity and
		// the sentence's closing bracket. The bracket comes off and the space
		// it left has to come off with it -- otherwise the entry records
		// "id=… " with a trailing space, and tf.Approves compares an identity
		// byte for byte, so it would clear nothing while reading as an
		// approval (tf.identityValue has single spaces and none at either
		// end).
		{"the copied tail with a space before the bracket", strings.TrimSuffix(tail, ")") + " )"},
		{"the identity bare, as the sentence names it", strings.TrimSuffix(tail, ")")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			if out, _, err := execute(t, "allow-delete", addr+"="+tc.before); err != nil {
				t.Fatalf("the text a refusal printed was refused: %v\n%s", err, out)
			}
			got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
			if !ok {
				t.Fatal("nothing was written")
			}
			var obj struct {
				Version int              `json:"version"`
				Entries []tf.DeleteEntry `json:"entries"`
			}
			if err := json.Unmarshal([]byte(got), &obj); err != nil {
				t.Fatalf("the object is not a delete list (%v):\n%s", err, got)
			}
			want := tf.ApprovalFor(rc)
			if len(obj.Entries) != 1 || obj.Entries[0] != want {
				t.Errorf("the object holds %+v, want the one entry that approves the removal (%+v)", obj.Entries, want)
			}
			if ok, why := tf.Approves(obj.Entries, rc); !ok {
				t.Errorf("the entry recorded from the paste does not approve the removal: %s", why)
			}
		})
	}

	// A parenthesis the sentence's bracket being taken off does not account
	// for: this is not an identity, and it is refused rather than recorded.
	for _, tc := range []struct{ name, before string }{
		// One bracket comes off and no more, and only at that end: a second
		// bracket is a text this command has no reading of, and so is one in
		// the middle of an identity.
		{"a second bracket", tail + ")"},
		{"a bracket inside the identity", "type=google_storage_bucket name=(fmgd-data)"},
		{"a bracketed identity and the sentence's bracket", "(" + tail + ")"},
	} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			out, _, err := execute(t, "allow-delete", addr+"="+tc.before)
			if err == nil {
				t.Fatalf("an argument that is not an identity was accepted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if strings.HasPrefix(tc.before, "type=") && !strings.Contains(err.Error(), "parenthesis") {
				t.Errorf("the refusal does not say what is wrong with it: %v", err)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
		})
	}
}

// TestAllowDeleteCommandRefusesAnIdentityTooLongToMatch: this command cannot
// tell which removals a plan shows, but it can tell when an identity is one no
// removal is ever identified by — a refusal prints an identity of at most
// tf.RemovalIdentityPrintMax characters, and the gate refuses a longer one
// rather than matching it — so recording it would write an entry the owner
// reads as an approval and that clears nothing.
func TestAllowDeleteCommandRefusesAnIdentityTooLongToMatch(t *testing.T) {
	r := allowDeleteRig(t)
	identity := "type=google_storage_bucket_object project=proj-1234 name=" + strings.Repeat("x", tf.RemovalIdentityPrintMax)
	if len(identity) <= tf.RemovalIdentityPrintMax {
		t.Fatalf("the fixture identity is %d characters, not past the bound (%d)", len(identity), tf.RemovalIdentityPrintMax)
	}
	arg := dataApproval.address + "=" + identity
	out, _, err := execute(t, "allow-delete", arg)
	if err == nil {
		t.Fatalf("an identity no removal is identified by was accepted:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(len(identity))) || !strings.Contains(err.Error(), strconv.Itoa(tf.RemovalIdentityPrintMax)) {
		t.Errorf("the refusal does not say how long the identity is and what the bound is: %v", err)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
}

// TestAllowDeleteCommandTrimsWhatAStripExposes: the marks a paste carries are
// stripped in whatever order the paste has them, and a strip can expose
// whitespace that the one after it then sits behind — a message a terminal
// wrapped puts a newline between the identity and the sentence's closing
// bracket, and a space there is ordinary. What is recorded is compared with a
// plan's identity byte for byte (tf.Approves), so a character left on the end
// of the copy is an approval of nothing: the loop the delete list works by —
// refuse, copy, approve — would not close, and nothing would say so.
//
// Review finding: the strip took the bracket off and did not trim what taking
// it off exposed, so "… id=data-bucket )" recorded an identity with a space on
// its end, which no plan's identity equals.
func TestAllowDeleteCommandTrimsWhatAStripExposes(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	rc := refusalRemoval("fmgd-data")
	bare := tf.RemoveIdentityText(rc)
	for _, tc := range []struct{ name, before string }{
		{"a space between the identity and the sentence's bracket", bare + " )"},
		{"a newline there, as a wrapped message leaves it", bare + "\n)"},
		{"a tab there", bare + "\t)"},
		{"a quoted identity and a space before the bracket", `"` + bare + `" )`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			if out, _, err := execute(t, "allow-delete", addr+"="+tc.before); err != nil {
				t.Fatalf("the text a refusal printed was refused: %v\n%s", err, out)
			}
			got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
			if !ok {
				t.Fatal("nothing was written")
			}
			var obj struct {
				Version int              `json:"version"`
				Entries []tf.DeleteEntry `json:"entries"`
			}
			if err := json.Unmarshal([]byte(got), &obj); err != nil {
				t.Fatalf("the object is not a delete list (%v):\n%s", err, got)
			}
			want := tf.ApprovalFor(rc)
			if len(obj.Entries) != 1 || obj.Entries[0] != want {
				t.Errorf("the object holds %+v, want the one entry that approves the removal (%+v)", obj.Entries, want)
			}
			if ok, why := tf.Approves(obj.Entries, rc); !ok {
				t.Errorf("the entry recorded from the paste does not approve the removal: %s", why)
			}
		})
	}
}

// TestAllowDeleteCommandRefusesAnIdentityNoPlanCouldHaveWritten: an identity is
// escaped text (tf.identityValue), so one is made of printable ASCII with a
// single space between two of its parts and no other whitespace at all — a
// space inside a value is written \x20, and a newline, a tab or an escape byte
// is written as an escape of its own — and the gate compares that text byte for
// byte. A copy that has been wrapped, re-indented or typed by hand can carry a
// character outside that set, and an entry holding one equals no identity a
// plan produces: the owner reads it as an approval and it clears nothing. It is
// refused here, loudly, rather than written.
//
// The set is closed at BOTH ends, and the last two rows are that upper end:
// DEL (0x7f), the byte just past printable ASCII and the one a terminal or a
// copy out of a pager leaves behind without showing anything, and 0x80, a byte
// of no printable ASCII at all. They are here because the bound was pinned from
// below only — every other row is a byte under 0x21 — so `c > 0x7e` could be
// widened to `c > 0x7f`, which admits DEL, and then past that, with this test
// still green.
func TestAllowDeleteCommandRefusesAnIdentityNoPlanCouldHaveWritten(t *testing.T) {
	for _, tc := range []struct{ name, before string }{
		{"a newline inside the identity", "type=google_storage_bucket\nid=data-bucket"},
		{"a tab between two parts", "type=google_storage_bucket\tid=data-bucket"},
		{"two spaces in a row between two parts", "type=google_storage_bucket  id=data-bucket"},
		{"an escape byte a message carried", "type=google_storage_bucket id=data-\x1bbucket"},
		{"a DEL byte a copy carried", "type=google_storage_bucket id=data-\x7fbucket"},
		{"a byte past ASCII a copy carried", "type=google_storage_bucket id=data-\x80bucket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			out, _, err := execute(t, "allow-delete", dataApproval.address+"="+tc.before)
			if err == nil {
				t.Fatalf("an identity no plan writes was recorded:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "printable ASCII") {
				t.Errorf("the refusal does not say what an identity is made of: %v", err)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
		})
	}

	// The other direction: an identity whose space is escaped, which is what a
	// refusal prints for a name that holds one, is still recorded. The refusal
	// above is about the shape an identity has and not about escaping.
	r := allowDeleteRig(t)
	rc := refusalRemoval("fmgd data")
	if _, _, err := execute(t, "allow-delete", rc.Address+"="+tf.RemoveIdentityText(rc)); err != nil {
		t.Fatalf("the identity a refusal prints was refused: %v", err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok || !strings.Contains(got, `\x20`) {
		t.Errorf("the object does not hold the escaped space of the name (found=%v): %s", ok, got)
	}
}

// TestAllowDeleteCommandTakesTheQuotesOffAWholeQuotedArgument: a person who
// quotes the whole argument — at a shell of their own, or in a message someone
// else quoted — arrives with one quote at each end of the ARGUMENT, and
// stripping only the identity half's pair left the other quote on the address:
// the entry recorded was `"module.managed.google_storage_bucket.fmgd_data=…`, an
// address no plan has, and it was written silently. The pair is taken off
// around the argument, before the cut at the first "=", and the identity half
// keeps the strip it had.
//
// A quote at one end only is not that pair, on either half: no address begins
// or ends with one (a quote is in an address only inside an index key, where
// the "]" closes it), there is no way to tell which end a lost pair was on, and
// the address half was the one this command never looked at — so the argument
// is refused rather than recorded as the address that quote would make it.
func TestAllowDeleteCommandTakesTheQuotesOffAWholeQuotedArgument(t *testing.T) {
	const addr = "module.managed.google_storage_bucket.fmgd_data"
	rc := refusalRemoval("fmgd-data")
	bare := tf.RemoveIdentityText(rc)
	for _, tc := range []struct{ name, arg string }{
		{"the whole argument quoted", `"` + addr + `=` + bare + `"`},
		{"the whole argument quoted, with the sentence's bracket inside the quotes", `"` + addr + `=` + bare + `)"`},
		{"the whole argument quoted, with the bracket after the quotes", `"` + addr + `=` + bare + `")`},
		{"the whole argument quoted, with the shell's spaces around it", "  " + `"` + addr + `=` + bare + `"` + "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			if out, _, err := execute(t, "allow-delete", tc.arg); err != nil {
				t.Fatalf("a quoted argument was refused: %v\n%s", err, out)
			}
			got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
			if !ok {
				t.Fatal("nothing was written")
			}
			var obj struct {
				Version int              `json:"version"`
				Entries []tf.DeleteEntry `json:"entries"`
			}
			if err := json.Unmarshal([]byte(got), &obj); err != nil {
				t.Fatalf("the object is not a delete list (%v):\n%s", err, got)
			}
			want := tf.ApprovalFor(rc)
			if len(obj.Entries) != 1 || obj.Entries[0] != want {
				t.Errorf("the object holds %+v, want the one entry that approves the removal (%+v)", obj.Entries, want)
			}
			if ok, why := tf.Approves(obj.Entries, rc); !ok {
				t.Errorf("the entry recorded from the paste does not approve the removal: %s", why)
			}
		})
	}

	// A quote that is not one of a pair: not a mark this command strips, and an
	// entry holding one at the address's end is an entry about an address no
	// plan has. And a text that needs two pairs — one around the whole argument
	// and one around the identity inside it — is a text this command has not
	// read: it strips one pair, so what is left is not an identity, and it is
	// refused rather than recorded as the tail of the second one.
	for _, tc := range []struct{ name, arg string }{
		{"a quote at the address's start alone", `"` + addr + `=` + bare},
		{"a quote left on each half", `"` + addr + `"="` + bare + `"`},
		{"a quote at the address's end", addr + `"=` + bare},
		{"a pair around the whole argument and another around the identity", `"` + addr + `="` + bare + `""`},
	} {
		t.Run("refused "+tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			out, _, err := execute(t, "allow-delete", tc.arg)
			if err == nil {
				t.Fatalf("an argument with an unpaired quote on the address was accepted:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "address") {
				t.Errorf("the refusal does not name the address half: %v", err)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
		})
	}
}

// TestAllowDeleteCommandRefusesAnAddressCutInsideItsIndexKey: an argument's two
// halves are cut at its FIRST "=", and a Terraform address may hold an "=" of
// its own inside an index key (google_x.y["a=b"]), so an address with one is an
// argument that cannot be read: the cut lands inside the address. What that
// leaves is one thing this command can see without the plan — the key is still
// open where the identity begins — and an address a plan writes closes every
// bracket it opens, so the half is refused rather than recorded as an entry
// about the truncated address, which no plan has and which approves nothing.
//
// Review finding: the doc promised this case was refused because the half after
// the cut does not begin with "type=", which is not so for an address whose key
// spells "=type=…": that half does begin with it, every other check passed, and
// the entry was recorded silently, about the truncated address.
func TestAllowDeleteCommandRefusesAnAddressCutInsideItsIndexKey(t *testing.T) {
	const identity = "type=google_storage_bucket project=proj-1234 name=data id=data-bucket"
	for _, tc := range []struct{ name, addr string }{
		{"the key spells an identity, as in the finding", `google_x.y["k=type=google_storage_bucket"]`},
		{"the key holds a plain =", `module.web.google_storage_bucket.data["a=b"]`},
		{"the key is left open by the cut", `google_x.y["k`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := allowDeleteRig(t)
			out, _, err := execute(t, "allow-delete", tc.addr+"="+identity)
			if err == nil {
				t.Fatalf("an address the cut landed inside was recorded:\n%s", out)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			if !strings.Contains(err.Error(), "index key") {
				t.Errorf("the refusal does not name the index key the cut landed in: %v", err)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
		})
	}

	// The other direction, and the residual the doc states rather than implies:
	// an address whose own text, after its first "=", spells out what an identity
	// looks like AND closes its bracket before the separator — here a key holding
	// a whole identity — is an argument this command cannot tell from
	// ADDRESS=IDENTITY. The half after the cut begins with "type=", the address it
	// leaves closes every bracket it opens, and what is recorded is an entry
	// about the TRUNCATED address: no plan has it, so it approves nothing, and
	// nothing in the argument says which of the two this is — only the plan
	// could. If this ever starts being refused, the doc's paragraph about the
	// residual has to change with it.
	r := allowDeleteRig(t)
	rc := refusalRemoval("fmgd-data")
	const truncated = `google_x.y["k"]`
	arg := truncated + `=type=google_secret_manager_secret project=proj-1234 name=s id=s"]` + "=" + tf.RemoveIdentityText(rc)
	if out, _, err := execute(t, "allow-delete", arg); err != nil {
		t.Fatalf("an address whose key spells an identity was refused (%v):\n%s\nif this is now the behaviour, the doc and this test change together", err, out)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("nothing was written")
	}
	var obj struct {
		Version int              `json:"version"`
		Entries []tf.DeleteEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("the object is not a delete list (%v):\n%s", err, got)
	}
	if len(obj.Entries) != 1 || obj.Entries[0].Address != truncated {
		t.Fatalf("the object holds %+v, want one entry about the truncated address %q", obj.Entries, truncated)
	}
	if approved, why := tf.Approves(obj.Entries, rc); approved {
		t.Errorf("the entry the residual records approves the removal: %s", why)
	}
}
