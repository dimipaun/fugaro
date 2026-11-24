package cli

import (
	"context"
	"strings"
	"testing"
)

// TestStampOpenRefusesHTTP2Debug: the fence reads the stamp from a root hook
// that guards nothing by itself, which makes this the first Google call of
// commands that were purely local before it, so the seam carries the guard every
// other cloud open carries. Without it the token refresh and the GET would be
// printed to stderr, bearer token included, under GODEBUG=http2debug.
func TestStampOpenRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	_, err := stampOpen(context.Background(), "https://demo-default-rtdb.firebaseio.com", false)
	if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("stampOpen under GODEBUG=http2debug = %v, want the GODEBUG refusal every cloud open makes", err)
	}
}
