package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
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

// TestListIsNeverRetriedIntoASecondCall pins the shape of the walk: a refusal
// is told once and never re-asked, and an answer that fits one page is one
// call. A retry loop, or a second credential tried after the first is refused,
// fails here.
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
