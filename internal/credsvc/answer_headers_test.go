package credsvc

import (
	"net/http"
	"testing"
)

// The service's own answer headers are the service's to write: an upstream
// that sends one (the probes, the tier) must not have it copied on.
func TestCopyAnswerHeadersDropsTheUpstreamsFugaroHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("X-Fugaro-Probed", "A,B")
	src.Set("X-Fugaro-Tier", "A")
	src.Set("Content-Type", "application/json")
	dst := http.Header{}
	copyAnswerHeaders(dst, src, "")
	if dst.Get("X-Fugaro-Probed") != "" || dst.Get("X-Fugaro-Tier") != "" {
		t.Errorf("an upstream's X-Fugaro-* header was copied on: %v", dst)
	}
	if dst.Get("Content-Type") != "application/json" {
		t.Errorf("an ordinary header was dropped: %v", dst)
	}
}
