package runner

import (
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// A gateway halt keeps the scope the lease's refusal named.
func TestGatewayHaltCarriesScope(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ reason, scope, want string }{
		{"repo_daily_cap", "repo", "repo"},
		{"global_daily_cap", "global", "global"},
		{"kill_switch", "global", "global"},
		{"kill_switch", "repo", "repo"},
		{"run_cap", "", "run"},
	} {
		got := gatewayHalt(gateway.Halt{Reason: c.reason, Scope: c.scope, Detail: "d", At: at})
		if got.Scope != c.want || got.Reason != runstore.HaltReason(c.reason) || got.Detail != "d" {
			t.Errorf("%s/%s: %+v, want scope %q", c.reason, c.scope, got, c.want)
		}
	}
}
