package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// This file is about the text `fugaro managed apply` and `fugaro
// allow-delete` print that is not this program's: the project's own name and
// its GCP project out of the project config, the runs bucket the config names,
// the root path the applying session passed, the name the checkout it is in
// declares, the addresses an owner's object holds, the account and the
// impersonation URL the cloud answers with, the path and the type of the
// operator's own credential file, and the body of an API's failed request.
//
// Every one of them is printed by a refusal or by a listing, and every one of
// them is bounded before it is (safetext.Field, safetext.FieldErr): an escape
// sequence, a bidi control, a forged newline or ten thousand characters in any
// of them must not reach the terminal as itself. The four classes are the ones
// the rest of this feature's tests feed each site, so a class that passes
// somewhere and fails here is a gap and not a difference of taste.

// hostileCLIValues is the four classes, one per entry.
func hostileCLIValues() map[string]string {
	return map[string]string{
		"C1 CSI":        string(rune(0x9b)) + "31mred",
		"bidi override": "a" + string(rune(0x202e)) + "gnp.exe",
		"newline":       "backend error\n  and a forged line",
		"10k":           strings.Repeat("z", 10000),
	}
}

// noRawHostile fails when out carries a control or bidi character as itself,
// more lines than the lines this print site is known to emit, or a line longer
// than a sentence of this program's own carrying bounded values can be.
//
// The line limit is not optional: a newline is a legitimate separator and so
// is exempt from the character scan above, which means the count is the only
// check that can see a hostile value forging a line of this program's output.
// A limit of zero would skip exactly that check, so the limit is required and
// refused when it is not a limit at all.
//
// The length bound is not FieldMax: one sentence legitimately holds several
// bounded values (the root and the error about it, the project and the bucket
// and the list's own refusal), so anything up to a handful of fields is this
// program's own text, while a value that reached the terminal uncut is ten
// thousand characters on one line and cannot be mistaken for it.
func noRawHostile(t *testing.T, what, out string, lines int) {
	t.Helper()
	if lines < 1 {
		t.Fatalf("%s: no line limit given, so a forged newline would go unseen", what)
	}
	for _, r := range out {
		if (r >= 0x80 && r <= 0x9f) || r == 0x202e || (r < 0x20 && r != '\n') {
			t.Errorf("%s carries %U:\n%q", what, r, out)
			return
		}
	}
	// A trailing newline is the end of the last line, not an empty one.
	if n := strings.Count(strings.TrimSuffix(out, "\n"), "\n") + 1; n != lines {
		t.Errorf("%s is %d lines, want exactly %d:\n%q", what, n, lines, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 4*safetext.FieldMax {
			t.Errorf("%s has a %d-rune line: a value was printed uncut:\n%q", what, len([]rune(line)), line)
			return
		}
	}
}

// yamlQuoted is v as a YAML double-quoted scalar: every class above is
// writable in a config file, which is where two of these values come from.
func yamlQuoted(v string) string { return strconv.Quote(v) }

// TestTheApplysRootRefusalIsBoundedEndToEnd: through the real command line,
// the real config loader and the rig's cloud. ROOT is the one hostile value a
// session can hand this command — the values in the project's own config are
// the loader's, and it refuses anything but a bucket name, a project ID or a
// project name — and it is quoted by the refusal that is printed before the
// cloud is touched at all.
func TestTheApplysRootRefusalIsBoundedEndToEnd(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			noAgentSession(t)
			// The rig's config and cloud, so the refusal reached is the
			// root's and not the loader's.
			managedBudgetRig(t)
			out, errOut, err := execute(t, "managed", "apply", filepath.Join(t.TempDir(), v))
			if err == nil {
				t.Fatalf("a root that is not there was applied:\n%s%s", out, errOut)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			// The refusal itself, and this command's own output beside it —
			// checked apart so that the refusal's line count is its own: a
			// hostile root forging a line would add one to a count that no
			// longer depends on how much else was printed.
			noRawHostile(t, name+" in the apply's refusal", err.Error(), 1)
			noRawHostile(t, name+" in the apply's output", out+errOut, 2)
			if !strings.Contains(err.Error(), "the root") {
				t.Errorf("the refusal is not the one about the root: %v", err)
			}
		})
	}
}

// TestTheRootsRefusalBoundsTheRootAndTheCheckoutsProject: ROOT is the applying
// session's own argument and the project the checkout declares is read out of
// a file in that root — the two values this command prints before it touches
// the cloud.
func TestTheRootsRefusalBoundsTheRootAndTheCheckoutsProject(t *testing.T) {
	testutil.IsolateGit(t)
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject}
	for name, v := range hostileCLIValues() {
		t.Run(name+" in the root", func(t *testing.T) {
			_, err := managedRoot(context.Background(), lc, filepath.Join(t.TempDir(), v))
			if err == nil {
				t.Fatal("a root that is there was accepted")
			}
			noRawHostile(t, name+" in the root's refusal", err.Error(), 1)
		})
		t.Run(name+" in the checkout's project", func(t *testing.T) {
			// A checkout of its own whose fugaro.yaml names another project:
			// the refusal names both, and the one out of the file is hostile.
			dir := t.TempDir()
			testutil.Git(t, dir, "init", "-q")
			root := filepath.Join(dir, "infra")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			yaml := "project: " + yamlQuoted(v) + "\n"
			if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := managedRoot(context.Background(), lc, root)
			if err == nil {
				t.Fatal("a root in another project's checkout was accepted")
			}
			if !strings.Contains(err.Error(), "checkout of project") {
				t.Fatalf("the refusal is not the one about the checkout's project: %v", err)
			}
			noRawHostile(t, name+" in the checkout's refusal", err.Error(), 1)
		})
	}
}

// TestManagedAskBoundsThePromptAndStillReadsTheTypedName: the question is
// printed with the project's own name twice, and the answer is compared with
// that name exactly — the bound is on the text and never on the comparison, or
// a name the bound shortened could never be typed back.
func TestManagedAskBoundsThePromptAndStillReadsTheTypedName(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			lc := &localcfg.Config{Name: v}
			cmd := newManagedApplyCmd()
			var out strings.Builder
			cmd.SetErr(&out)
			cmd.SetIn(strings.NewReader(v + "\n"))
			ok, err := managedAsk(cmd, lc)("apply the saved plan (sha256 abc)")
			if err != nil {
				t.Fatal(err)
			}
			// This program's own two lines — the warning and the question —
			// and never more, however many lines the name would have added.
			noRawHostile(t, name+" in the prompt", out.String(), 2)
			if !strings.Contains(out.String(), safetext.Field(v)) {
				t.Errorf("the prompt does not carry the name it is about, bounded:\n%q", out.String())
			}
			// And the comparison is on the name itself, never on the bounded
			// text: a name a terminal can deliver is read back exactly, and one
			// with a newline in it cannot be typed at all, so it is not
			// confirmed — the fail-closed answer, not the bound's.
			if want := !strings.ContainsAny(v, "\n\r"); ok != want {
				t.Errorf("the typed name was read as %v, want %v: the bound is not on the comparison", ok, want)
			}
		})
	}
}

// TestTheApplyRefusalsMapBoundsEveryValue: managedListErr and managedApplyErr
// are where the apply's own errors become the sentence a person reads, and
// every value they interpolate — the project, the runs bucket, the root, and
// the error's own text — is bounded there.
func TestTheApplyRefusalsMapBoundsEveryValue(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			lc := &localcfg.Config{Name: v, GCPProject: v}
			for what, err := range map[string]error{
				"a refused list": managedListErr(lc, v, fmt.Errorf("%w: %s", managed.ErrRefused, v)),
				"a denied list":  managedListErr(lc, v, &googleapi.Error{Code: http.StatusForbidden, Message: v}),
				"an unknown one": managedListErr(lc, v, errors.New(v)),
				"the apply's":    managedApplyErr(lc, v, managedmode.Result{}, fmt.Errorf("boom: %s", v)),
				"the gate's":     managedApplyErr(lc, v, managedmode.Result{}, fmt.Errorf("%w: %s", managedmode.ErrRefused, v)),
			} {
				noRawHostile(t, name+" in "+what, err.Error(), 1)
			}
		})
	}
}

// TestPrintEntriesCapsTheListingAndSaysWhatItLeftOut: the listing of an
// owner's object is bounded twice — each entry (address and identity) a field,
// and the count of entries capped, with what was left out said rather than
// silently dropped.
func TestPrintEntriesCapsTheListingAndSaysWhatItLeftOut(t *testing.T) {
	hostile := hostileCLIValues()
	many := make([]tf.DeleteEntry, 0, safetext.FieldListMax+5)
	for i := 0; i < safetext.FieldListMax+5; i++ {
		many = append(many, tf.DeleteEntry{
			Address: fmt.Sprintf("google_storage_bucket.a%02d", i),
			Before:  fmt.Sprintf("type=google_storage_bucket id=a%02d", i)})
	}
	// One entry of each class inside the cap, so the sanitising is exercised
	// as well as the count — in the address, and in the identity beside it.
	last := 0
	for _, v := range hostile {
		many[last].Address = "google_storage_bucket." + v
		many[last].Before = "type=google_storage_bucket id=" + v
		last++
	}
	want := make([]string, 0, len(many))
	for _, e := range many {
		want = append(want, e.Address+"="+e.Before)
	}
	var b strings.Builder
	printEntries(&b, many)
	out := b.String()
	noRawHostile(t, "the listing", out, safetext.FieldListMax+1)

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != safetext.FieldListMax+1 {
		t.Fatalf("the listing is %d lines, want %d entries and the note", len(lines), safetext.FieldListMax+1)
	}
	if note := "(" + safetext.FieldListNote(5) + ")"; lines[len(lines)-1] != note {
		t.Errorf("the listing ends with %q, want %q", lines[len(lines)-1], note)
	}
	for i, line := range lines[:safetext.FieldListMax] {
		if w := safetext.Field(want[i]); line != w {
			t.Errorf("entry %d is %q, want %q", i, line, w)
		}
	}
	if strings.Contains(out, many[len(many)-1].Address) {
		t.Errorf("the listing carries %q, past the cap", many[len(many)-1].Address)
	}

	// Under the cap nothing is said about leaving anything out.
	b.Reset()
	printEntries(&b, many[:3])
	if got := b.String(); strings.Contains(got, "more") || len(strings.Split(strings.TrimSuffix(got, "\n"), "\n")) != 3 {
		t.Errorf("a listing under the cap is %q, want three lines and no note", got)
	}
}

// TestTheListingPrintsWholeLinesWholeAndCutsOnlyWhatItCannot: the switch
// between FieldUncut and Field sits at the listing's own line bound, and it is
// in that direction — inside the bound the entry is printed as itself, even
// past safetext.FieldMax, where Field would cut a text its owner can no longer
// paste back; past the bound it is Field's, cut and marked.
//
// The write half is pinned with it, because the two together are the reason the
// bound is where it is: the address this command records is unbounded
// (listingLineMax says why), so an entry past the bound is one the listing may
// be printing something this command wrote, and the cut there is the honest
// answer rather than a line nothing could have produced.
func TestTheListingPrintsWholeLinesWholeAndCutsOnlyWhatItCannot(t *testing.T) {
	inside := tf.DeleteEntry{
		Address: "google_storage_bucket." + strings.Repeat("a", 200),
		Before:  "type=google_storage_bucket id=" + strings.Repeat("b", 400),
	}
	line := inside.Address + "=" + inside.Before
	if len(line) <= safetext.FieldMax || len(line) > listingLineMax {
		t.Fatalf("the fixture line is %d characters, and this test is about the range between FieldMax (%d) and listingLineMax (%d)", len(line), safetext.FieldMax, listingLineMax)
	}
	var b strings.Builder
	printEntries(&b, []tf.DeleteEntry{inside})
	if got, want := b.String(), line+"\n"; got != want {
		t.Errorf("a line inside the listing's bound printed as\n%q\nwant the entry itself\n%q\nit is FieldUncut's: Field would cut an entry an owner then cannot paste back", got, want)
	}

	// Past the bound the line is Field's — cut at FieldMax, ending in Field's
	// own marker, and not the entry.
	over := tf.DeleteEntry{
		Address: "google_storage_bucket." + strings.Repeat("c", listingLineMax),
		Before:  "type=google_storage_bucket id=x",
	}
	b.Reset()
	printEntries(&b, []tf.DeleteEntry{over})
	got, want := strings.TrimSuffix(b.String(), "\n"), safetext.Field(over.Address+"="+over.Before)
	if got != want {
		t.Errorf("a line past the listing's bound printed as %d characters, want Field's %d", len([]rune(got)), len([]rune(want)))
	}
	if !strings.HasSuffix(got, "…") || len([]rune(got)) >= len(over.Address) {
		t.Errorf("a line past the bound is %q, want a text cut short and marked as cut", got)
	}

	// And that entry is one this command would have written: approveArgument
	// bounds the identity and not the address (deliberately — listingLineMax).
	if e, err := approveArgument(over.Address + "=" + over.Before); err != nil || e.Address != over.Address {
		t.Errorf("approving an address past the listing's bound = %q, %v; want the entry recorded: the address is the plan's own, this list is a managed project's only way to approve its delete, and a bound here would lock that removal away", e.Address, err)
	}
}

// TestTheListingPromisesAPasteOnlyForTheHalfItWrote: printEntries offers its
// lines as text the owner pastes back, and that is true of the identity half —
// the command that recorded the entry escaped it (tf.RemovalIdentity.String
// escapes every byte the printers rewrite), so safetext.FieldUncut's rewrites
// are no-ops there and the line carries the identity back byte for byte. The
// address half is the plan's own, neither escaped nor bounded (listingLineMax),
// so an address holding a character the printers rewrite is printed as they
// rewrite it: that line is not the entry, and pasting it back records an entry
// about an address the plan does not have — the gate compares by exact
// equality, so it approves nothing, loudly, at the apply. The listing says
// which half it can promise this for; this pins the two halves apart.
func TestTheListingPromisesAPasteOnlyForTheHalfItWrote(t *testing.T) {
	written := tf.DeleteEntry{
		Address: "module.managed.google_storage_bucket.fmgd_data",
		Before:  `type=google_storage_bucket project=proj-1234 name=fmgd\x20data id=fmgd\x20data-bucket`,
	}
	var b strings.Builder
	printEntries(&b, []tf.DeleteEntry{written})
	line := strings.TrimSuffix(b.String(), "\n")
	if want := written.Address + "=" + written.Before; line != want {
		t.Fatalf("the listing printed %q, want the entry itself %q", line, want)
	}
	if e, err := approveArgument(line); err != nil || e != written {
		t.Errorf("the listing's own line pasted back = %+v, %v; want the entry it printed (%+v)", e, err, written)
	}

	raw := tf.DeleteEntry{
		Address: "google_storage_bucket.a\tb",
		Before:  "type=google_storage_bucket id=data-bucket",
	}
	b.Reset()
	printEntries(&b, []tf.DeleteEntry{raw})
	got := strings.TrimSuffix(b.String(), "\n")
	if want := safetext.FieldUncut(raw.Address) + "=" + raw.Before; got != want {
		t.Fatalf("the listing printed %q, want the address as the printers leave it (%q)", got, want)
	}
	if got == raw.Address+"="+raw.Before {
		t.Fatal("the listing printed the address as the entry holds it: this test is about an address a printer rewrites")
	}
	e, err := approveArgument(got)
	if err != nil {
		t.Fatalf("the line the listing printed was refused: %v", err)
	}
	if want := safetext.FieldUncut(raw.Address); e.Address != want || e.Address == raw.Address {
		t.Errorf("the line the listing printed pasted back as the address %q, want the printed one %q (the entry's own is %q)", e.Address, want, raw.Address)
	}
}

// TestAllowDeleteListingIsCappedEndToEnd: the command's own listing, through
// the real command line: the header counts the whole list, the listing stops
// at the cap, and the line after it says how many it did not print.
func TestAllowDeleteListingIsCappedEndToEnd(t *testing.T) {
	r := allowDeleteRig(t)
	total := safetext.FieldListMax + 5
	entries := make([]tf.DeleteEntry, 0, total)
	for i := 0; i < total; i++ {
		entries = append(entries, tf.DeleteEntry{
			Address: fmt.Sprintf("google_storage_bucket.a%02d", i),
			Before:  fmt.Sprintf("type=google_storage_bucket id=a%02d", i),
		})
	}
	body, err := json.Marshal(managed.DeleteList{Version: managed.AllowDeleteVersion, GCPProject: initProject, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, body)
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, fmt.Sprintf("approves %d entry(ies)", total)) {
		t.Fatalf("the listing was not read as one, so the print is not exercised:\n%q", out)
	}
	if !strings.Contains(out, "("+safetext.FieldListNote(5)+")") {
		t.Errorf("the listing does not say how many it left out:\n%q", out)
	}
	shown := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "google_storage_bucket.a") {
			shown++
		}
	}
	if shown != safetext.FieldListMax {
		t.Errorf("the listing shows %d entries, want %d", shown, safetext.FieldListMax)
	}
	if last := entries[total-1].Address; strings.Contains(out, last) {
		t.Errorf("the listing carries %q, past the cap:\n%q", last, out)
	}
}

// TestTheCredentialFilesOwnTextIsBounded: the path the operator's credential
// is at is an environment value and the type inside the file is whatever the
// file says — the two values this command prints about a credential it refuses
// to use.
func TestTheCredentialFilesOwnTextIsBounded(t *testing.T) {
	isolateCredentialDir(t)
	for name, v := range hostileCLIValues() {
		t.Run(name+" in the path", func(t *testing.T) {
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), v))
			_, _, err := writeImpersonationFile("https://example.test/impersonation")
			if err == nil {
				t.Fatal("a credential file that is not there was read")
			}
			noRawHostile(t, name+" in the ADC refusal", err.Error(), 1)
		})
		t.Run(name+" in the type", func(t *testing.T) {
			adc := filepath.Join(t.TempDir(), "adc.json")
			body, err := json.Marshal(map[string]any{"type": v})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(adc, body, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)
			_, _, err = writeImpersonationFile("https://example.test/impersonation")
			if err == nil {
				t.Fatal("a credential of another kind was used")
			}
			noRawHostile(t, name+" in the credential type's refusal", err.Error(), 1)
		})
	}
}

// TestTheImpersonationsOwnWordsAreBounded: the impersonation URL is built from
// the account and read back, and the API's answer about a failed call is
// another program's text — both are printed by the refusals the apply maps.
func TestTheImpersonationsOwnWordsAreBounded(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name+" in the URL", func(t *testing.T) {
			_, err := impersonationAccount("https://iamcredentials.example.test/v1/projects/-/serviceAccounts/" + v)
			if err == nil {
				t.Fatal("a URL naming no account was read as one")
			}
			noRawHostile(t, name+" in the URL's refusal", err.Error(), 1)
		})
		t.Run(name+" in the API's answer", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(v))
			}))
			defer srv.Close()
			imp := deployerImpersonator{
				self: "user:operator@example.com", endpoint: srv.URL, hc: srv.Client(),
				ts: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("op-")}),
			}
			_, err := imp.accessToken(context.Background(), managedmode.DeployerAccount(initProject))
			if err == nil {
				t.Fatal("a 5xx from the impersonation API produced a token")
			}
			noRawHostile(t, name+" in the impersonation API's refusal", err.Error(), 1)
		})
		t.Run(name+" in the status line", func(t *testing.T) {
			// The reason phrase of a status line is the far end's own text:
			// Go hands it to a caller as it arrived, so the refusal prints it
			// bounded like the body. A raw listener answers with it, because
			// no http.Handler can write a status line of its own.
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				fmt.Fprintf(c, "HTTP/1.1 403 %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", v)
			}()
			imp := deployerImpersonator{
				self: "user:operator@example.com", endpoint: "http://" + ln.Addr().String(), hc: &http.Client{},
				ts: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: fixtureToken("op-")}),
			}
			_, err = imp.accessToken(context.Background(), managedmode.DeployerAccount(initProject))
			if err == nil {
				t.Fatal("a 403 from the impersonation API produced a token")
			}
			noRawHostile(t, name+" in the status line's refusal", err.Error(), 1)
		})
	}
}

// TestTheCallersOwnRefusalIsBounded: who the run is comes from a userinfo
// endpoint's answer — the status line included, which is that server's own
// text — and the error chain carrying it is printed by this command's
// refusal. The read itself is a package variable (its own comment: so tests
// never reach the network), which is what lets the chain be hostile here.
func TestTheCallersOwnRefusalIsBounded(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			old := authenticatedUser
			t.Cleanup(func() { authenticatedUser = old })
			authenticatedUser = func(context.Context, *localcfg.Config) (string, error) {
				return "", fmt.Errorf("asking who you are: 403 %s", v)
			}
			_, _, err := managedCaller(context.Background(), &localcfg.Config{Name: initProjectName, GCPProject: initProject})
			if err == nil {
				t.Fatal("a caller that could not be read was accepted")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			noRawHostile(t, name+" in the caller's refusal", err.Error(), 1)
		})
	}
}

// TestAllowDeleteProjectNumberErrorIsBounded: the project number is read from
// the cloud before anything else, and a read that fails answers with the API's
// own words (a 5xx body, a proxy's page). `fugaro managed apply` and `fugaro
// managed flag` print that text as one bounded field; allow-delete, the third
// command of the same feature, prints it the same way, and still exits as a
// remote failure.
func TestAllowDeleteProjectNumberErrorIsBounded(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			r := allowDeleteRig(t)
			r.crm.FailGet(initProject, http.StatusBadRequest, "FAILED_PRECONDITION", "backend error"+v)
			out, errOut, err := execute(t, "allow-delete")
			if err == nil {
				t.Fatalf("the project number was not read, and the command went on:\n%s%s", out, errOut)
			}
			if ExitCode(err) != ExitRemoteError {
				t.Errorf("exit %d, want %d: the bound must not change what kind of failure this is", ExitCode(err), ExitRemoteError)
			}
			noRawHostile(t, name+" in allow-delete's project-number error", err.Error(), 1)
		})
	}
}

// TestManagedFlagProjectNumberErrorIsBounded: the same read, in the flag
// command (Task 6.1), which prints it before the confirmation is asked for.
func TestManagedFlagProjectNumberErrorIsBounded(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			r := managedFlagRig(t)
			r.crm.FailGet(initProject, http.StatusBadRequest, "FAILED_PRECONDITION", "backend error"+v)
			terminal(t, true)
			out, errOut, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
			if err == nil {
				t.Fatalf("the project number was not read, and the flag went on:\n%s%s", out, errOut)
			}
			noRawHostile(t, name+" in the flag's project-number error", err.Error(), 1)
		})
	}
}

// TestManagedFlagErrBoundsEveryValue: managedFlagErr is where the flag's write
// failures become the sentence the owner reads, and every value it
// interpolates — the project, the runs bucket, and the error's own text,
// whether an API's or the writer's — is bounded there, as managedListErr's are
// for the apply. The default branch is the one a wrapped provider error
// reaches: it keeps its chain (the exit code is still remote) and loses its
// lines.
func TestManagedFlagErrBoundsEveryValue(t *testing.T) {
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			// The GCP project and the bucket are the validated project ID and
			// the name built from it (the command refuses any other before it
			// writes), so the hostile value is the project's own name and the
			// error's text; each sentence then carries at most three bounded
			// values, which is what the line-length check measures.
			lc := &localcfg.Config{Name: v, GCPProject: initProject}
			for what, err := range map[string]error{
				"an already-marked one": managedFlagErr(lc, initRunsBucket, fmt.Errorf("%w: %s", managed.ErrAlreadyMarked, v)),
				"a refused one":         managedFlagErr(lc, initRunsBucket, fmt.Errorf("%w: %s", managed.ErrRefused, v)),
				"a denied one":          managedFlagErr(lc, initRunsBucket, &googleapi.Error{Code: http.StatusForbidden, Message: v}),
				"an unknown one":        managedFlagErr(lc, initRunsBucket, errors.New(v)),
			} {
				noRawHostile(t, name+" in "+what, err.Error(), 1)
			}
			if err := managedFlagErr(lc, initRunsBucket, errors.New(v)); ExitCode(err) != ExitRemoteError {
				t.Errorf("an unknown write failure exits %d, want %d", ExitCode(err), ExitRemoteError)
			}
		})
	}
}

// TestConfirmTypedStrictBoundsTheNameAndComparesTheNameItself: allow-delete's
// confirmation prints the project's name twice and compares the typed line
// with the name itself — the bound is on the text and never on the comparison.
func TestConfirmTypedStrictBoundsTheNameAndComparesTheNameItself(t *testing.T) {
	terminal(t, true)
	for name, v := range hostileCLIValues() {
		t.Run(name, func(t *testing.T) {
			lc := &localcfg.Config{Name: v}
			cmd := newManagedApplyCmd()
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(v + "\n"))
			err := confirmTypedStrict(cmd, lc, "why", "what")
			noRawHostile(t, name+" in the confirmation", out.String(), 2)
			if want := !strings.ContainsAny(v, "\n\r"); (err == nil) != want {
				t.Errorf("the typed name was confirmed=%v, want %v (err %v): the bound is not on the comparison", err == nil, want, err)
			}
		})
	}
}
