package billing

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// fakeGcloud is the injected gcloud of W4.2: it records what it was asked and
// answers with canned output, so no test in this file runs a binary — and none
// of them needs gcloud on PATH.
type fakeGcloud struct {
	// out is what the runner answers with.
	out []byte
	// err is what the runner fails with, when set.
	err error
	// absent takes gcloud off PATH.
	absent bool
	// calls is the argv of every run, lookups the number of PATH probes.
	calls   [][]string
	lookups int
}

// seams is the fallback's two injected ends.
func (f *fakeGcloud) seams() Gcloud {
	return Gcloud{Run: f.run, LookPath: f.lookPath}
}

func (f *fakeGcloud) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func (f *fakeGcloud) lookPath(file string) (string, error) {
	f.lookups++
	if file != GcloudBin {
		return "", fmt.Errorf("the PATH probe asked for %q, want %q", file, GcloudBin)
	}
	if f.absent {
		return "", exec.ErrNotFound
	}
	return "/usr/bin/" + file, nil
}

// options is one billing question for the tests: the API the fake stands at,
// the project the guided line names, and the injected gcloud.
func options(creds Credentials, project string, g *fakeGcloud) Options {
	return Options{Credentials: creds, Project: project, Gcloud: g.seams()}
}

// refusedAPI is a billing fake whose account list the API refuses, so the
// fallback is the only source that can answer. A fake that answered instead
// would make the two sources indistinguishable in these tests.
func refusedAPI(t *testing.T) *gcpfake.Billing {
	t.Helper()
	f := gcpfake.NewBilling(t)
	refuseAccounts(f)
	return f
}

// gcloudTable is `gcloud billing accounts list`'s own table: a header, then
// one row per account, each column padded to the header's width. It is built
// from the values a test passes, so no account in it is a literal.
func gcloudTable(rows ...[4]string) []byte {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%-22s%-34s%-7s%s\n", "ACCOUNT_ID", "NAME", "OPEN", "MASTER_ACCOUNT_ID"))
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-22s%-34s%-7s%s\n", r[0], r[1], r[2], r[3]))
	}
	return []byte(b.String())
}

// listed0 is what gcloud prints for a caller with no accounts at all: no
// table, and no error either.
const listed0 = "Listed 0 items.\n"

func TestGcloudFallbackParsesOpenAccounts(t *testing.T) {
	open, closed := accountID(t, "open"), accountID(t, "closed")
	g := &fakeGcloud{out: gcloudTable(
		[4]string{open, "the operator's own account", "True", ""},
		[4]string{closed, "one that was closed", "False", ""},
	)}

	got, source, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != open {
		t.Fatalf("the fallback's list: %+v (a closed account cannot be linked, so it is not offered)", got)
	}
	if got[0].DisplayName != "the operator's own account" || !got[0].Open {
		t.Errorf("the account: %+v, want the display name gcloud printed and open", got[0])
	}
	if source != SourceGcloud {
		t.Errorf("the source: %q, want %q", source, SourceGcloud)
	}
}

// TestGcloudFallbackReadsTheColumnsTheHeaderNames pins that the table is read
// by the header's own column offsets, not by counting tokens from either end:
// a display name holds spaces, and an account that belongs to a reseller
// carries a master account id in the last column. A parser that splits on
// whitespace reads the wrong cells here.
func TestGcloudFallbackReadsTheColumnsTheHeaderNames(t *testing.T) {
	id, master := accountID(t, "one"), accountID(t, "master")
	g := &fakeGcloud{out: gcloudTable([4]string{id, "a name with spaces", "True", master})}

	got, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("the fallback's list: %+v, want %s", got, id)
	}
	if got[0].DisplayName != "a name with spaces" {
		t.Errorf("the display name: %q, want the whole name, spaces and all", got[0].DisplayName)
	}
}

// TestGcloudFallbackSkipsAnEntryItCannotRead pins that the fallback drops what
// List drops: an entry whose ID is not an account id is not offered, because
// the id ends up in a typed confirmation and in a printed gcloud line.
func TestGcloudFallbackSkipsAnEntryItCannotRead(t *testing.T) {
	good, bad := accountID(t, "good"), strings.ToLower(accountID(t, "bad"))
	g := &fakeGcloud{out: gcloudTable(
		[4]string{bad, "an account whose ID is not one", "True", ""},
		[4]string{good, "the operator's own account", "True", ""},
	)}

	got, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != good {
		t.Errorf("the fallback's list: %+v, want only %s", got, good)
	}
}

// TestGcloudFallbackNamesAreMadeSafeToPrint pins that the display name reaches
// the wizard through safetext: it is gcloud's output, which is text from
// outside this program, and it is printed by number next to the id (W4.3).
func TestGcloudFallbackNamesAreMadeSafeToPrint(t *testing.T) {
	name := "the operator's\x07 own account"
	g := &fakeGcloud{out: gcloudTable([4]string{accountID(t, "one"), name, "True", ""})}

	got, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the fallback's list: %+v", got)
	}
	if strings.ContainsRune(got[0].DisplayName, '\x07') {
		t.Errorf("a control character from gcloud's answer reached the account: %q", got[0].DisplayName)
	}
	if got[0].DisplayName != "the operator's? own account" {
		t.Errorf("the display name: %q, want gcloud's answer made safe to print", got[0].DisplayName)
	}
}

// TestGcloudFallbackWithNoAccounts pins the empty answer: a caller with no
// accounts at all is not a failure of the fallback, and nothing is invented
// for it.
func TestGcloudFallbackWithNoAccounts(t *testing.T) {
	g := &fakeGcloud{out: []byte(listed0)}

	got, source, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a caller with no accounts: %+v", got)
	}
	if source != SourceGcloud {
		t.Errorf("the source: %q, want %q (gcloud answered; it answered with none)", source, SourceGcloud)
	}
}

func TestGcloudFallbackUsedOnlyWhenTheAPIIsUnavailable(t *testing.T) {
	// The API answers, so the fallback is never consulted — not even when
	// gcloud is on PATH and would answer with something else. The two sources
	// can disagree, and the API is the one the wizard prefers.
	f := gcpfake.NewBilling(t)
	id := accountID(t, "from-the-api")
	f.AddAccountNamed(id, "the account the API knows", true)
	g := &fakeGcloud{out: gcloudTable([4]string{accountID(t, "from-gcloud"), "the account gcloud knows", "True", ""})}

	got, source, err := ListWithFallback(context.Background(), options(fakeCreds(f), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Errorf("the list: %+v, want the API's own answer", got)
	}
	if source != SourceAPI {
		t.Errorf("the source: %q, want %q", source, SourceAPI)
	}
	if len(g.calls) != 0 {
		t.Errorf("gcloud was run after the API answered: %q", g.calls)
	}
	if g.lookups != 0 {
		t.Errorf("PATH was probed %d times after the API answered, want 0", g.lookups)
	}

	// An API that answers with no accounts is still an answer: an operator who
	// has none is told so, and gcloud is not asked the same question.
	empty := gcpfake.NewBilling(t)
	got, source, err = ListWithFallback(context.Background(), options(fakeCreds(empty), "fugaro-demo", g))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || source != SourceAPI {
		t.Errorf("an API answering with no accounts: %+v / %q, want none from the API", got, source)
	}
	if len(g.calls) != 0 {
		t.Errorf("gcloud was run after the API answered with no accounts: %q", g.calls)
	}
}

// TestGcloudFallbackSaysWhichSourceAnswered pins the sentence W4.3 prints: the
// two sources can disagree, so a list is never shown without the name of the
// one that produced it.
func TestGcloudFallbackSaysWhichSourceAnswered(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	_, api, err := ListWithFallback(context.Background(), options(fakeCreds(f), "fugaro-demo", &fakeGcloud{}))
	if err != nil {
		t.Fatal(err)
	}
	_, gl, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo",
		&fakeGcloud{out: gcloudTable([4]string{accountID(t, "one"), "the operator's own account", "True", ""})}))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		source Source
		want   string
	}{
		{api, "Cloud Billing API"},
		{gl, "gcloud billing accounts list"},
	} {
		if !strings.Contains(c.source.String(), c.want) {
			t.Errorf("the source %q is named %q, want the words %q", c.source, c.source.String(), c.want)
		}
	}
	if api == gl {
		t.Errorf("both sources answered as %q: the wizard cannot say which one did", api)
	}
}

// TestGcloudFallbackAbsentSaysWhichLineToRun is the case Check 111 calls a
// failure mode: neither source can answer, so the fallback hands the caller
// today's guided line — the one internal/cli's guided() prints — and the API's
// own reason stays underneath it.
func TestGcloudFallbackAbsentSaysWhichLineToRun(t *testing.T) {
	g := &fakeGcloud{absent: true}

	got, source, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if len(got) != 0 {
		t.Errorf("neither source answered with accounts: %+v", got)
	}
	if source != SourceNone {
		t.Errorf("the source: %q, want none", source)
	}
	var e *ErrNoSource
	if !errors.As(err, &e) {
		t.Fatalf("neither source answering: %v, want *ErrNoSource", err)
	}
	if want := "gcloud billing projects link fugaro-demo --billing-account=BILLING_ACCOUNT_ID"; e.Guided != want {
		t.Errorf("the guided line: %q, want %q", e.Guided, want)
	}
	if !strings.Contains(err.Error(), e.Guided) {
		t.Errorf("the error does not carry the line to run: %v", err)
	}
	if !strings.Contains(err.Error(), "gcloud is not on PATH") {
		t.Errorf("the error does not say why the fallback was not tried: %v", err)
	}
	var p *ErrNoPermission
	if !errors.As(err, &p) {
		t.Errorf("the API's own failure is not carried: %v", err)
	}
	if len(g.calls) != 0 {
		t.Errorf("a gcloud that is not on PATH was run anyway: %q", g.calls)
	}
}

// TestGcloudFallbackGuidedLineQuotesTheProject pins that the line handed to
// the operator is a command someone pastes into a shell: the project goes
// through shellword, exactly as internal/cli's guided() prints it.
func TestGcloudFallbackGuidedLineQuotesTheProject(t *testing.T) {
	e := &ErrNoSource{Guided: Guided("a project with spaces")}
	if want := "gcloud billing projects link 'a project with spaces' --billing-account=BILLING_ACCOUNT_ID"; e.Guided != want {
		t.Errorf("the guided line: %q, want %q", e.Guided, want)
	}
}

// TestGcloudFallbackRefusesAnAnswerItCannotRead pins that an answer which is
// not the table — a refusal, a prompt, a version warning — is a failure of the
// fallback rather than an empty list, and that gcloud's own words are not
// carried into the error: they can hold an account's name, and this package
// prints no part of what gcloud listed.
func TestGcloudFallbackRefusesAnAnswerItCannotRead(t *testing.T) {
	out := "ERROR: (gcloud.billing.accounts.list) The caller does not have permission\n"
	g := &fakeGcloud{out: []byte(out)}

	got, source, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if len(got) != 0 || source != SourceNone {
		t.Errorf("an unreadable answer: %+v / %q, want no accounts and no source", got, source)
	}
	var e *ErrNoSource
	if !errors.As(err, &e) {
		t.Fatalf("an unreadable answer: %v, want *ErrNoSource", err)
	}
	if e.Gcloud == nil {
		t.Error("the fallback answered and was not recorded as the reason")
	}
	if strings.Contains(err.Error(), "ERROR:") {
		t.Errorf("gcloud's answer reached the error: %v", err)
	}
}

// TestGcloudFallbackRefusesAFailedRun pins the other half: gcloud refusing to
// run at all — no auth, no such command — is the guided line too, not an
// empty list.
func TestGcloudFallbackRefusesAFailedRun(t *testing.T) {
	g := &fakeGcloud{err: errors.New("exit status 1")}

	got, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g))
	if len(got) != 0 {
		t.Errorf("a gcloud that failed answered with accounts: %+v", got)
	}
	var e *ErrNoSource
	if !errors.As(err, &e) {
		t.Fatalf("a failed gcloud: %v, want *ErrNoSource", err)
	}
	if e.Gcloud == nil || !strings.Contains(err.Error(), "gcloud did not answer") {
		t.Errorf("the error does not say the fallback failed: %v", err)
	}
}

// TestGcloudArgsAreOnlyTheListCommand pins what the fallback runs: the list
// command, once, and no flag at all — no --format (the table is what the
// operator sees too), no --quiet, no filter.
func TestGcloudArgsAreOnlyTheListCommand(t *testing.T) {
	g := &fakeGcloud{out: gcloudTable([4]string{accountID(t, "one"), "the operator's own account", "True", ""})}

	if _, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g)); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 1 {
		t.Fatalf("gcloud was run %d times, want 1: %q", len(g.calls), g.calls)
	}
	if want := []string{"billing", "accounts", "list"}; !slices.Equal(g.calls[0], want) {
		t.Errorf("the command: %q, want %q", g.calls[0], want)
	}
}

// TestGcloudOutputNeverStored pins the last of D6's bounds for the fallback:
// what gcloud printed lives in the returned accounts and nowhere else. The
// file is checked for the two ways a Go file stores or prints such a thing —
// os, and log — and for a fmt call that writes; then the call is made twice,
// because an answer kept between calls is kept somewhere.
func TestGcloudOutputNeverStored(t *testing.T) {
	src, err := os.ReadFile("gcloud.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gcloud.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing gcloud.go: %v", err)
	}
	for _, im := range file.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		switch path {
		case "os", "log", "log/slog":
			t.Errorf("gcloud.go imports %q: this file is where gcloud's answer arrives, and it may neither store nor log it", path)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if strings.HasPrefix(sel.Sel.Name, "Print") || strings.HasPrefix(sel.Sel.Name, "Fprint") {
			t.Errorf("gcloud.go calls %s (%s): gcloud's answer is never written out", sel.Sel.Name, fset.Position(call.Pos()))
		}
		return true
	})

	g := &fakeGcloud{out: gcloudTable([4]string{accountID(t, "one"), "the operator's own account", "True", ""})}
	for i := 0; i < 2; i++ {
		if _, _, err := ListWithFallback(context.Background(), options(fakeCreds(refusedAPI(t)), "fugaro-demo", g)); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.calls) != 2 {
		t.Errorf("the second question ran gcloud %d times in all, want 2: the answer is not kept between calls", len(g.calls))
	}
}

// TestGcloudChildNeverOffersToEnableAnAPI pins the one setting the fallback's
// child is run with: `billing accounts list` is a read, and a gcloud that
// finds an API off must not offer to turn it on — the offer is a change to a
// project this question was not asked about, and a prompt in a run whose
// answer is not being read is a hang. It is appended to the environment gcloud
// is given, so it also wins over the operator's own setting of it.
func TestGcloudChildNeverOffersToEnableAnAPI(t *testing.T) {
	const setting = "CLOUDSDK_CORE_SHOULD_PROMPT_TO_ENABLE_API"

	environ := []string{"PATH=/usr/bin", setting + "=true"}
	got := gcloudEnv(environ)
	if want := []string{"PATH=/usr/bin", setting + "=true", setting + "=false"}; !slices.Equal(got, want) {
		t.Errorf("gcloudEnv(%q) = %q, want %q", environ, got, want)
	}
	if environ[1] != setting+"=true" {
		t.Errorf("gcloudEnv changed the slice it was given (%q): it is exec.Cmd's own copy of the environment, and a caller's slice is not its to write", environ)
	}

	// And the child is run with it: a gcloudEnv nobody calls is a setting
	// nobody sets. Read out of the file the way this file's own privacy pin
	// reads it, so a mutation that drops the wiring is not a compile error
	// nobody notices but a FAIL here.
	src, err := os.ReadFile("gcloud.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gcloud.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing gcloud.go: %v", err)
	}
	wired := false
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Env" {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "gcloudEnv" {
				wired = true
			}
		}
		return true
	})
	if !wired {
		t.Errorf("nothing in gcloud.go sets an Env from gcloudEnv: the child runs with the environment it inherits, prompt to enable an API and all")
	}
}
