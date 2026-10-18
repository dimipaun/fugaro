package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// a line of its own the hostile value forged, more than lines lines, or a line
// longer than a sentence of this program's own carrying bounded values can be.
//
// The length bound is not FieldMax: one sentence legitimately holds several
// bounded values (the root and the error about it, the project and the bucket
// and the list's own refusal), so anything up to a handful of fields is this
// program's own text, while a value that reached the terminal uncut is ten
// thousand characters on one line and cannot be mistaken for it.
func noRawHostile(t *testing.T, what, out string, lines int) {
	t.Helper()
	for _, r := range out {
		if (r >= 0x80 && r <= 0x9f) || r == 0x202e || (r < 0x20 && r != '\n') {
			t.Errorf("%s carries %U:\n%q", what, r, out)
			return
		}
	}
	// A trailing newline is the end of the last line, not an empty one.
	if n := strings.Count(strings.TrimSuffix(out, "\n"), "\n") + 1; lines > 0 && n > lines {
		t.Errorf("%s is %d lines, want at most %d:\n%q", what, n, lines, out)
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
			_, _ = managedBudgetRig(t)
			out, errOut, err := execute(t, "managed", "apply", filepath.Join(t.TempDir(), v))
			if err == nil {
				t.Fatalf("a root that is not there was applied:\n%s%s", out, errOut)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
			}
			noRawHostile(t, name+" in the apply's refusal", err.Error()+"\n"+out+errOut, 0)
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
			noRawHostile(t, name+" in the root's refusal", err.Error(), 0)
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
			noRawHostile(t, name+" in the checkout's refusal", err.Error(), 0)
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
				noRawHostile(t, name+" in "+what, err.Error(), 0)
			}
		})
	}
}

// TestPrintAddressesCapsTheListingAndSaysWhatItLeftOut: the listing of an
// owner's object is bounded twice — each address a field, and the count of
// entries capped, with what was left out said rather than silently dropped.
func TestPrintAddressesCapsTheListingAndSaysWhatItLeftOut(t *testing.T) {
	hostile := hostileCLIValues()
	many := make([]string, 0, safetext.FieldListMax+5)
	for i := 0; i < safetext.FieldListMax+5; i++ {
		many = append(many, fmt.Sprintf("google_storage_bucket.a%02d", i))
	}
	// One entry of each class inside the cap, so the sanitising is exercised
	// as well as the count.
	last := 0
	for _, v := range hostile {
		many[last] = "google_storage_bucket." + v
		last++
	}
	var b strings.Builder
	printAddresses(&b, many)
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
		if want := safetext.Field(many[i]); line != want {
			t.Errorf("entry %d is %q, want %q", i, line, want)
		}
	}
	if strings.Contains(out, many[len(many)-1]) {
		t.Errorf("the listing carries %q, past the cap", many[len(many)-1])
	}

	// Under the cap nothing is said about leaving anything out.
	b.Reset()
	printAddresses(&b, many[:3])
	if got := b.String(); strings.Contains(got, "more") || len(strings.Split(strings.TrimSuffix(got, "\n"), "\n")) != 3 {
		t.Errorf("a listing under the cap is %q, want three lines and no note", got)
	}
}

// TestAllowDeleteListingIsCappedEndToEnd: the command's own listing, through
// the real command line: the header counts the whole list, the listing stops
// at the cap, and the line after it says how many it did not print.
func TestAllowDeleteListingIsCappedEndToEnd(t *testing.T) {
	r := allowDeleteRig(t)
	total := safetext.FieldListMax + 5
	addresses := make([]string, 0, total)
	for i := 0; i < total; i++ {
		addresses = append(addresses, fmt.Sprintf("google_storage_bucket.a%02d", i))
	}
	body, err := json.Marshal(map[string]any{"version": 1, "gcp_project": initProject, "addresses": addresses})
	if err != nil {
		t.Fatal(err)
	}
	r.gcs.Put(initRunsBucket, managed.AllowDeleteObject, body)
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, fmt.Sprintf("allows %d address(es)", total)) {
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
	if last := addresses[total-1]; strings.Contains(out, last) {
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
			noRawHostile(t, name+" in the ADC refusal", err.Error(), 0)
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
			noRawHostile(t, name+" in the credential type's refusal", err.Error(), 0)
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
			noRawHostile(t, name+" in the URL's refusal", err.Error(), 0)
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
	}
}
