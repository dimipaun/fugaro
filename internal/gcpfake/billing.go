package gcpfake

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Billing is a fake of the Cloud Billing call Fugaro makes: a project's
// billing info (projects/<id>/billingInfo). A project it doesn't know is a
// 403, as the real API answers a project the caller can't read.
type Billing struct {
	*Server

	mu      sync.Mutex
	enabled map[string]bool
	account map[string]string // the account linked to a project
	// accounts are the billing accounts the caller may link (id: open); with
	// none registered any account is accepted.
	accounts map[string]bool
	crm      *CRM
	hidden   map[string]bool
	links    []Link
	failLink *apiError
}

// Link is an updateBillingInfo the fake accepted.
type Link struct{ Project, Account string }

var billingInfoRE = regexp.MustCompile(`^/v1/projects/([^/:]+)/billingInfo$`)

// NewBilling starts a Cloud Billing fake that lives until the test ends.
func NewBilling(t *testing.T) *Billing {
	t.Helper()
	f := &Billing{enabled: map[string]bool{}, account: map[string]string{}, accounts: map[string]bool{}, hidden: map[string]bool{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SetBilling makes project known, with billing on or off.
func (f *Billing) SetBilling(project string, enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[project] = enabled
}

// UseCRM makes the projects the Resource Manager fake knows (the ones a
// test created) known here too, without billing until linked.
func (f *Billing) UseCRM(c *CRM) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.crm = c
}

// SetSuspended makes the project keep a link to account while billing is
// disabled (a suspended or closed account).
func (f *Billing) SetSuspended(project, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[project], f.account[project] = false, "billingAccounts/"+account
}

// HideBilling makes reads and links of the project's billing answer 403, as
// for a caller without billing.resourceAssociations.list.
func (f *Billing) HideBilling(project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hidden[project] = true
}

// AddAccount makes billing account id one the caller may link projects to;
// a closed account refuses a link with FAILED_PRECONDITION.
func (f *Billing) AddAccount(id string, open bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accounts[id] = open
}

// FailLinks makes every link answer code, status and msg. A code of 0 lifts it.
func (f *Billing) FailLinks(code int, status, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failLink = nil
	if code != 0 {
		f.failLink = &apiError{code: code, status: status, msg: msg}
	}
}

// Attempts counts the writes (PUTs) received, accepted or refused: a test
// that says "nothing was written" asserts this is zero, so it fails when a
// write is attempted even though the fake refuses it.
func (f *Billing) Attempts() int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet {
			n++
		}
	}
	return n
}

// Links are the updateBillingInfo calls accepted so far.
func (f *Billing) Links() []Link {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Link(nil), f.links...)
}

func (f *Billing) handle(w http.ResponseWriter, r *http.Request, req []byte) {
	m := billingInfoRE.FindStringSubmatch(r.URL.Path)
	if m == nil || (r.Method != http.MethodGet && r.Method != http.MethodPut) {
		f.unhandled(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, known := f.enabled[m[1]]; !known && f.crm != nil && f.crm.Has(m[1]) {
		f.enabled[m[1]] = false
	}
	if f.hidden[m[1]] {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	if r.Method == http.MethodPut {
		f.link(w, m[1], req)
		return
	}
	on, ok := f.enabled[m[1]]
	if !ok {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	body := map[string]any{"name": "projects/" + m[1] + "/billingInfo", "projectId": m[1], "billingEnabled": on}
	if on || f.account[m[1]] != "" {
		body["billingAccountName"] = cmpOr(f.account[m[1]], "billingAccounts/000000-000000-000000")
	}
	writeJSON(w, http.StatusOK, body)
}

func (f *Billing) link(w http.ResponseWriter, project string, body []byte) {
	if _, ok := f.enabled[project]; !ok {
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission")
		return
	}
	if f.account[project] != "" || f.enabled[project] {
		// The fake refuses a replacement with 403. It does not model the real
		// API's documented replace behaviour (updateBillingInfo on a linked
		// project replaces the link for a caller allowed to), so it proves
		// only that init does not try, which is what Attempts asserts.
		writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller may not replace the project's existing billing link")
		return
	}
	if f.failLink != nil {
		writeError(w, f.failLink.code, f.failLink.status, f.failLink.msg)
		return
	}
	var in struct {
		BillingAccountName string `json:"billingAccountName"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "bad body")
		return
	}
	id := strings.TrimPrefix(in.BillingAccountName, "billingAccounts/")
	if len(f.accounts) > 0 {
		open, ok := f.accounts[id]
		switch {
		case !ok:
			writeError(w, http.StatusForbidden, "PERMISSION_DENIED", "The caller does not have permission to link to this billing account")
			return
		case !open:
			writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", "Billing account "+id+" is closed")
			return
		}
	}
	f.enabled[project], f.account[project] = true, in.BillingAccountName
	f.links = append(f.links, Link{Project: project, Account: id})
	writeJSON(w, http.StatusOK, map[string]any{"name": "projects/" + project + "/billingInfo", "projectId": project,
		"billingEnabled": true, "billingAccountName": in.BillingAccountName})
}
