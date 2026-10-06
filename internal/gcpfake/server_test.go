package gcpfake

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServerDropsRequestsWhoseBodyNeverArrived: a client that gives up while
// it is still sending (a context deadline, say) leaves a truncated body. The
// fake must treat that as a client that went away, not run its handler on
// half a JSON document and report its own parse error as a test failure.
func TestServerDropsRequestsWhoseBodyNeverArrived(t *testing.T) {
	l := NewLogging(t)
	failed := false
	l.failf = func(format string, args ...any) { failed = true }

	conn, err := net.Dial("tcp", strings.TrimPrefix(l.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	// Promise 200 bytes, send 10, and hang up.
	fmt.Fprintf(conn, "POST /v2/entries:list HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{\"resourceN")
	conn.Close()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if failed {
		t.Error("the fake reported a failure for a request whose body never arrived")
	}
	if n := len(l.Requests()); n != 0 {
		t.Errorf("%d requests recorded, want 0: the client never finished sending", n)
	}
}

func TestRefusePathRefusesOnlyThatPath(t *testing.T) {
	f := NewIAM(t)
	f.AddRole("p", "r", "R", false)
	f.AddServiceAccount("p", "a@p.iam.gserviceaccount.com", "A")
	f.RefusePath("/roles/", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "denied")
	if r, err := http.Get(f.URL + "/v1/projects/p/roles/r"); err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("role: %v %v", r, err)
	}
	if r, err := http.Get(f.URL + "/v1/projects/p/serviceAccounts/a@p.iam.gserviceaccount.com"); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("account: %v %v", r, err)
	}
}
