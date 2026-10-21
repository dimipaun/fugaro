package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// accountID is a billing account ID built at run time from the test's name and
// tag — a constant digest, never a literal — so that nothing in this file is
// an account's. It has the API's shape: three groups of six uppercase
// hexadecimal digits.
func accountID(t *testing.T, tag string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + "/" + tag))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	return h[0:6] + "-" + h[6:12] + "-" + h[12:18]
}

// fakeCreds points List at a fake with no credentials at all: no test here
// needs a token, and none of them may reach Google.
func fakeCreds(f *gcpfake.Billing) Credentials {
	return Credentials{Endpoint: f.URL, NoAuth: true}
}

// refuseAccounts makes the fake refuse the list the way Google refuses a
// caller without billing.accounts.list.
func refuseAccounts(f *gcpfake.Billing) {
	f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "The caller does not have permission")
}

func TestListReturnsOpenAccountsOnly(t *testing.T) {
	f := gcpfake.NewBilling(t)
	open, closed := accountID(t, "open"), accountID(t, "closed")
	f.AddAccountNamed(open, "the operator's own account", true)
	f.AddAccountNamed(closed, "one that was closed", false)

	got, err := List(context.Background(), fakeCreds(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != open {
		t.Fatalf("the list: %+v (a closed account cannot be linked, so it is not offered)", got)
	}
}

func TestListReturnsIDAndDisplayName(t *testing.T) {
	f := gcpfake.NewBilling(t)
	id, name := accountID(t, "one"), "the operator's own account"
	f.AddAccountNamed(id, name, true)

	got, err := List(context.Background(), fakeCreds(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the list: %+v", got)
	}
	if got[0].ID != id || got[0].DisplayName != name || !got[0].Open {
		t.Errorf("the account: %+v, want %s / %q / open", got[0], id, name)
	}
}

func TestListRefusedWhenTheAPIIsDisabled(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	su := gcpfake.NewServiceUsage(t)
	su.Disable("cloudbilling.googleapis.com", f.Server)

	got, err := List(context.Background(), fakeCreds(f))
	if len(got) != 0 {
		t.Errorf("a disabled API answered with accounts: %+v", got)
	}
	var e *ErrDisabled
	if !errors.As(err, &e) {
		t.Fatalf("a disabled API: %v, want *ErrDisabled", err)
	}
	quota := strings.TrimPrefix(su.Consumer, "projects/")
	if e.QuotaProject != quota {
		t.Errorf("the quota project: %q, want %q", e.QuotaProject, quota)
	}
	if want := "gcloud services enable cloudbilling.googleapis.com --project " + quota; e.EnableCommand != want {
		t.Errorf("the enable command: %q, want %q", e.EnableCommand, want)
	}
	if msg := e.Error(); !strings.Contains(msg, quota) || !strings.Contains(msg, "cloudbilling.googleapis.com") {
		t.Errorf("the error names neither the project nor the API: %v", msg)
	}
}

// TestListQuotesTheQuotaProjectInTheEnableCommand pins that the enable line is
// a command someone pastes into a shell: the quota project goes through
// shellword, so a value that is not one safe word is quoted instead of
// splitting the line. Dropping the quoting prints it bare.
func TestListQuotesTheQuotaProjectInTheEnableCommand(t *testing.T) {
	f := gcpfake.NewBilling(t)
	disabledOn(t, f, "projects/a project with spaces")

	_, err := List(context.Background(), fakeCreds(f))
	var e *ErrDisabled
	if !errors.As(err, &e) {
		t.Fatalf("a disabled API: %v, want *ErrDisabled", err)
	}
	if e.QuotaProject != "a project with spaces" {
		t.Errorf("the quota project: %q", e.QuotaProject)
	}
	want := "gcloud services enable cloudbilling.googleapis.com --project 'a project with spaces'"
	if e.EnableCommand != want {
		t.Errorf("the enable command: %q, want %q", e.EnableCommand, want)
	}
}

// TestListWithNoAccounts pins the empty answer: a caller with no billing
// accounts at all is not an error, and nothing is invented for it.
func TestListWithNoAccounts(t *testing.T) {
	f := gcpfake.NewBilling(t)

	got, err := List(context.Background(), fakeCreds(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a caller with no accounts: %+v", got)
	}
}

func TestListRefusedWithoutThePermission(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	refuseAccounts(f)

	got, err := List(context.Background(), fakeCreds(f))
	if len(got) != 0 {
		t.Errorf("a refused list answered with accounts: %+v", got)
	}
	var e *ErrNoPermission
	if !errors.As(err, &e) {
		t.Fatalf("a 403 without SERVICE_DISABLED: %v, want *ErrNoPermission", err)
	}
	var d *ErrDisabled
	if errors.As(err, &d) {
		t.Errorf("a missing permission is not a disabled API: %v", err)
	}
	if msg := e.Error(); !strings.Contains(msg, "billing.accounts.list") {
		t.Errorf("the error does not name the permission: %v", msg)
	}
}

// disabledOn points a Service Usage fake at the billing fake with consumer as
// the project the SERVICE_DISABLED answer names, as projects/<number>.
func disabledOn(t *testing.T, f *gcpfake.Billing, consumer string) *gcpfake.ServiceUsage {
	t.Helper()
	su := gcpfake.NewServiceUsage(t)
	su.Consumer = consumer
	su.Disable("cloudbilling.googleapis.com", f.Server)
	return su
}

// TestListRefusedWithoutAQuotaProject pins the one project this package must
// never name as one to enable the API on: when the credentials name no quota
// project of their own, Google bills the call to its own project (the literal
// below, gcloud's default), and the answer's consumer is that project. An
// enable line for it would send the operator to a project that is not theirs.
func TestListRefusedWithoutAQuotaProject(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	disabledOn(t, f, "projects/764086051850")

	got, err := List(context.Background(), fakeCreds(f))
	if len(got) != 0 {
		t.Errorf("a refused list answered with accounts: %+v", got)
	}
	var e *ErrNoQuotaProject
	if !errors.As(err, &e) {
		t.Fatalf("a disable billed to Google's own project: %v, want *ErrNoQuotaProject", err)
	}
	var d *ErrDisabled
	if errors.As(err, &d) {
		t.Errorf("Google's own project was named as one to enable the API on: %v", err)
	}
	if msg := e.Error(); strings.Contains(msg, "764086051850") && !strings.Contains(msg, "not yours") {
		t.Errorf("the error names Google's project without saying it is not the operator's: %v", msg)
	}
	if !strings.Contains(e.Error(), "set-quota-project") {
		t.Errorf("the error does not say how to name a quota project: %v", e.Error())
	}
	if e.SetCommand != "gcloud auth application-default set-quota-project <your-project>" {
		t.Errorf("the fix: %q", e.SetCommand)
	}
}

// TestListClassifiesRefusalsByReason pins what a 403 that is not a disable is
// told as: the missing permission the wizard has an answer for keeps its own
// error, and every other reason the API names — USER_PROJECT_DENIED, an
// insufficient token scope — gets the generic one that quotes it, rather than
// all being told "you need billing.accounts.list".
func TestListClassifiesRefusalsByReason(t *testing.T) {
	for _, c := range []struct {
		name   string
		reason string
		perm   bool
		quoted string
	}{
		{"the missing permission", "IAM_PERMISSION_DENIED", true, ""},
		{"no reason at all", "", true, ""},
		{"the quota project rejected", "USER_PROJECT_DENIED", false, "USER_PROJECT_DENIED"},
		{"a token without the scope", "ACCESS_TOKEN_SCOPE_INSUFFICIENT", false, "ACCESS_TOKEN_SCOPE_INSUFFICIENT"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gcpfake.NewBilling(t)
			f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", c.reason, "refused")

			_, err := List(context.Background(), fakeCreds(f))
			var perm *ErrNoPermission
			var refused *ErrRefused
			switch {
			case c.perm && !errors.As(err, &perm):
				t.Fatalf("a %s refusal: %v, want *ErrNoPermission", c.name, err)
			case !c.perm && !errors.As(err, &refused):
				t.Fatalf("a %s refusal: %v, want *ErrRefused", c.name, err)
			case c.perm && errors.As(err, &refused):
				t.Errorf("a %s refusal was told as the generic one: %v", c.name, err)
			case !c.perm && errors.As(err, &perm):
				t.Errorf("a %s refusal was told as a missing billing.accounts.list: %v", c.name, err)
			}
			if c.quoted != "" && !strings.Contains(err.Error(), c.quoted) {
				t.Errorf("the refusal does not quote the reason the API gave: %v", err)
			}
			if c.perm && !strings.Contains(err.Error(), "billing.accounts.list") {
				t.Errorf("the missing-permission error does not name the permission: %v", err)
			}
		})
	}
}

// TestRefusalReasonIsMadeSafeToPrint pins that the reason the API gave reaches
// the terminal through safetext: it is a string from outside this program, and
// a newline or an escape sequence in it must not reach a printed line whole.
func TestRefusalReasonIsMadeSafeToPrint(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "TWO\nLINES", "refused")

	_, err := List(context.Background(), fakeCreds(f))
	var refused *ErrRefused
	if !errors.As(err, &refused) {
		t.Fatalf("the refusal: %v, want *ErrRefused", err)
	}
	if msg := err.Error(); strings.ContainsAny(msg, "\n\r") {
		t.Errorf("the reason's newline reached the error: %q", msg)
	}
	if !strings.Contains(err.Error(), "TWO?LINES") {
		t.Errorf("the reason is not the sanitized one: %q", err.Error())
	}
}

// TestListLeavesADisabledAPINamingNoProjectUntyped pins that "an API is off"
// is never answered with the missing-permission error. A SERVICE_DISABLED
// answer that names no quota project gives this package nothing to print an
// enable line for, so it stays as it came rather than sending the operator
// after roles/billing.viewer.
func TestListLeavesADisabledAPINamingNoProjectUntyped(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "SERVICE_DISABLED", "the API is disabled")

	_, err := List(context.Background(), fakeCreds(f))
	if err == nil {
		t.Fatal("a refused list returned no error")
	}
	var d *ErrDisabled
	var p *ErrNoPermission
	if errors.As(err, &d) || errors.As(err, &p) {
		t.Errorf("a disabled API naming no project is neither a named disable nor a missing permission: %v", err)
	}
	if !strings.Contains(err.Error(), "listing billing accounts") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

// TestListLeavesAnotherAPIsDisabledAnswerUntyped pins the other half of the
// same reading: a SERVICE_DISABLED answer naming a *different* API is not this
// package's API being off, so it is not an ErrDisabled either — the quota
// project in it belongs to that API's enable line, not Cloud Billing's.
func TestListLeavesAnotherAPIsDisabledAnswerUntyped(t *testing.T) {
	f := gcpfake.NewBilling(t)
	su := gcpfake.NewServiceUsage(t)
	su.Disable("firestore.googleapis.com", f.Server)

	_, err := List(context.Background(), fakeCreds(f))
	if err == nil {
		t.Fatal("a refused list returned no error")
	}
	var d *ErrDisabled
	if errors.As(err, &d) {
		t.Errorf("another API's disabled answer became ErrDisabled with %q", d.QuotaProject)
	}
}

// TestListSkipsAnEntryItCannotRead pins that a malformed answer is skipped
// rather than a panic: the API's own shape is not this package's to assume.
func TestListSkipsAnEntryItCannotRead(t *testing.T) {
	id := accountID(t, "one")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"billingAccounts":[null,{"name":"billingAccounts/` + id + `","displayName":"the operator's own account","open":true}]}`))
	}))
	defer srv.Close()

	got, err := List(context.Background(), Credentials{Endpoint: srv.URL, NoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Errorf("the list: %+v, want only %s", got, id)
	}
}

// TestListIsNeverRetriedIntoASecondCall pins the shape of the walk: one call
// per page and not one more, and no failure answered by asking again — not a
// refusal, and not a transient one either. A retry loop, or a second
// credential tried after the first is refused, fails here. (Following
// nextPageToken is a walk of pages, not a retry: TestListFollowsEveryPage
// covers it.)
func TestListIsNeverRetriedIntoASecondCall(t *testing.T) {
	refused := gcpfake.NewBilling(t)
	refuseAccounts(refused)
	if _, err := List(context.Background(), fakeCreds(refused)); err == nil {
		t.Fatal("a refused list returned no error")
	}
	if n := len(refused.Requests()); n != 1 {
		t.Errorf("a refused list made %d calls, want 1: %+v", n, refused.Requests())
	}

	one := gcpfake.NewBilling(t)
	one.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	if _, err := List(context.Background(), fakeCreds(one)); err != nil {
		t.Fatal(err)
	}
	if n := len(one.Requests()); n != 1 {
		t.Errorf("a one-page list made %d calls, want 1: %+v", n, one.Requests())
	}

	// The codes a retrying client would re-ask: the generated client sends
	// through gensupport.SendRequest, which carries no retry config, so each
	// of these is one call and one error. A client that started retrying —
	// here or in the SDK — makes a second call and fails this.
	for _, c := range []struct {
		code   int
		status string
	}{
		{http.StatusInternalServerError, "INTERNAL"},
		{http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"},
	} {
		f := gcpfake.NewBilling(t)
		f.RefusePath("/v1/billingAccounts", c.code, c.status, "", c.status)
		if _, err := List(context.Background(), fakeCreds(f)); err == nil {
			t.Fatalf("a %d list returned no error", c.code)
		}
		if n := len(f.Requests()); n != 1 {
			t.Errorf("a %d list made %d calls, want 1: %+v", c.code, n, f.Requests())
		}
	}
}

// TestListAsksOnlyForTheAccounts pins what the call is: the account list, a
// page of the API's own maximum size, and no filter — the closed accounts are
// dropped by this package, not by the API.
func TestListAsksOnlyForTheAccounts(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(accountID(t, "one"), "the operator's own account", true)
	if _, err := List(context.Background(), fakeCreds(f)); err != nil {
		t.Fatal(err)
	}
	reqs := f.Requests()
	if len(reqs) != 1 {
		t.Fatalf("the calls: %+v", reqs)
	}
	if reqs[0].Method != http.MethodGet || reqs[0].Path != "/v1/billingAccounts" {
		t.Errorf("the call: %s %s, want GET /v1/billingAccounts", reqs[0].Method, reqs[0].Path)
	}
	if !strings.Contains(reqs[0].Query, "pageSize=100") {
		// The API's own maximum page size, and its default: pinned as the
		// literal it is, so that changing it is a change someone made on
		// purpose.
		t.Errorf("the page size: %q, want pageSize=100", reqs[0].Query)
	}
	if strings.Contains(reqs[0].Query, "filter") {
		t.Errorf("the list was filtered (%q): the API's filter is for subaccounts, and the closed accounts are this package's to drop", reqs[0].Query)
	}
}

// TestListFollowsEveryPage pins that an operator with more accounts than one
// page holds is offered all of them: the walk follows nextPageToken instead of
// showing the first page as if it were the list.
func TestListFollowsEveryPage(t *testing.T) {
	f := gcpfake.NewBilling(t)
	ids := make([]string, 0, listPageSize+1)
	for i := 0; i <= listPageSize; i++ {
		id := accountID(t, strconv.Itoa(i))
		ids = append(ids, id)
		f.AddAccountNamed(id, "account "+strconv.Itoa(i), true)
	}

	got, err := List(context.Background(), fakeCreds(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ids) {
		t.Errorf("the list has %d accounts, want %d", len(got), len(ids))
	}
	if n := len(f.Requests()); n != 2 {
		t.Errorf("a list of %d accounts took %d calls, want 2: %+v", len(ids), n, f.Requests())
	}
}

// pages hands out one account per call, forever: tokens is the next page
// token the call-th answer carries ("" ends the walk), so a test can drive the
// walk's own limits from a server that never stops or never moves on.
func pages(t *testing.T, ids []string, tokens func(call int) string) *httptest.Server {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := map[string]any{"billingAccounts": []any{map[string]any{
			"name": "billingAccounts/" + ids[(calls-1)%len(ids)], "displayName": "account " + strconv.Itoa(calls), "open": true,
		}}}
		if next := tokens(calls); next != "" {
			body["nextPageToken"] = next
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestListRefusesARepeatedPageToken pins the walk's first limit: a server that
// answers the same token again is a loop, not a list, and it is refused rather
// than followed. Without the check the walk runs to the page bound instead.
func TestListRefusesARepeatedPageToken(t *testing.T) {
	ids := []string{accountID(t, "one"), accountID(t, "two")}
	srv := pages(t, ids, func(int) string { return "same" })

	got, err := List(context.Background(), Credentials{Endpoint: srv.URL, NoAuth: true})
	if err == nil {
		t.Fatalf("a repeated page token was followed to the end: %+v", got)
	}
	if !strings.Contains(err.Error(), "same") {
		t.Errorf("the error does not name the token it refused: %v", err)
	}
}

// TestListStopsAtThePageBound pins the walk's second limit: a server that hands
// out a fresh token forever is stopped at the bound — 100 pages, the literal
// below, so that raising it is a change someone made on purpose. The server
// runs out of tokens at 250, so a walk with no bound finishes instead of
// hanging, and this test fails on its own assertions.
func TestListStopsAtThePageBound(t *testing.T) {
	ids := []string{accountID(t, "one")}
	srv := pages(t, ids, func(call int) string {
		if call >= 250 {
			return ""
		}
		return strconv.Itoa(call)
	})

	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return http.DefaultTransport.RoundTrip(r)
	})}
	got, err := List(context.Background(), Credentials{Endpoint: srv.URL, NoAuth: true, HTTPClient: client})
	if err == nil {
		t.Fatalf("a list of endless pages finished with %d accounts", len(got))
	}
	if calls != 100 {
		t.Errorf("the walk made %d calls, want the bound of 100", calls)
	}
}

// roundTripFunc is a RoundTripper that counts what it passes on.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestListSkipsAnAccountWhoseIDIsNotOne pins that an ID this package cannot
// read is not passed on: the ID ends up in a typed confirmation and in a
// printed gcloud line, so an entry whose ID is not the API's own shape is
// dropped rather than offered.
func TestListSkipsAnAccountWhoseIDIsNotOne(t *testing.T) {
	f := gcpfake.NewBilling(t)
	good := accountID(t, "good")
	f.AddAccountNamed(good, "the operator's own account", true)
	for _, tag := range []string{"lower", "short"} {
		bad := accountID(t, tag)
		switch tag {
		case "lower":
			bad = strings.ToLower(bad)
		case "short":
			bad = bad[:11]
		}
		f.AddAccountNamed(bad, "an account whose ID is not one", true)
	}

	got, err := List(context.Background(), fakeCreds(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != good {
		t.Errorf("the list: %+v, want only %s", got, good)
	}
}

// TestOnlyTheWizardMayImportThisPackage is the head comment's "called from
// exactly one place", checked: it walks every non-test Go file of the module
// and fails on any package that imports this one without being on the list
// below. W4.3 is that one place — internal/cli's billing question — and adding
// it here is a deliberate change to the privacy story, not a step a new
// importer takes on its own.
func TestOnlyTheWizardMayImportThisPackage(t *testing.T) {
	allowed := map[string]bool{}
	root := filepath.Join("..", "..") // internal/billing -> the module root
	const self = "github.com/dimipaun/fugaro/internal/billing"
	importers := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The import is read from the file's syntax tree, so a comment that
		// mentions the path is not an importer.
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		imports := false
		for _, is := range f.Imports {
			if p, err := strconv.Unquote(is.Path.Value); err == nil && p == self {
				imports = true
			}
		}
		if !imports {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		importers[dir] = true
		if !allowed[dir] {
			t.Errorf("%s imports %s: it may be imported only by %v (W4.3's billing question)", rel, self, allowed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	for dir := range allowed {
		if !importers[dir] {
			t.Errorf("the list allows %s to import this package, and it does not: the list is stale", dir)
		}
	}
}

// TestListRefusesNoAuthWithoutAnEndpoint pins the one combination that would
// put a credential-less call on the wire: NoAuth with nothing to point it at
// is refused before any client is built, so no test can reach Google's own
// endpoint by leaving Endpoint out.
func TestListRefusesNoAuthWithoutAnEndpoint(t *testing.T) {
	got, err := List(context.Background(), Credentials{NoAuth: true})
	if err == nil {
		t.Fatalf("no_auth with no endpoint: %v, want an error", got)
	}
	if !strings.Contains(err.Error(), "no_auth") {
		t.Errorf("the error does not name what is wrong: %v", err)
	}
}

// TestListLeavesOtherFailuresUntyped pins that the two typed errors are the
// only two: a bad request, and credentials that are missing or expired, are
// neither a disabled API nor a missing permission, and they come back as the
// API's own answer.
func TestListLeavesOtherFailuresUntyped(t *testing.T) {
	for _, c := range []struct {
		name   string
		code   int
		status string
	}{
		{"a bad request", http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"credentials the API rejected", http.StatusUnauthorized, "UNAUTHENTICATED"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gcpfake.NewBilling(t)
			f.RefusePath("/v1/billingAccounts", c.code, c.status, "", c.status)

			_, err := List(context.Background(), fakeCreds(f))
			if err == nil {
				t.Fatal("a refused list returned no error")
			}
			var d *ErrDisabled
			var p *ErrNoPermission
			if errors.As(err, &d) || errors.As(err, &p) {
				t.Errorf("%s (%d) is neither a disabled API nor a missing permission: %v", c.name, c.code, err)
			}
			if !strings.Contains(err.Error(), "listing billing accounts") {
				t.Errorf("the error does not say what failed: %v", err)
			}
		})
	}
}

// TestListLogsNoAccount pins the rule discardLogger is there for: with the
// Google SDK's logging switched on — GOOGLE_SDK_GO_LOGGING_LEVEL=debug makes a
// client log the body of every answer — nothing of the list reaches stderr.
// The client is built with a discarded logger for exactly this; dropping that
// option fails this test.
func TestListLogsNoAccount(t *testing.T) {
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "debug")
	f := gcpfake.NewBilling(t)
	id, name := accountID(t, "one"), "the operator's own account"
	f.AddAccountNamed(id, name, true)

	// The client's logger is built from os.Stderr when the service is
	// constructed, so the swap has to be in place before List runs.
	log, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = log
	defer func() { os.Stderr = old }()

	if _, err := List(context.Background(), fakeCreds(f)); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log.Name())
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, id) || strings.Contains(s, name) {
		t.Errorf("the client logged the account list to stderr: %s", s)
	}
}
