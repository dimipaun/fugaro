package agent

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestRedactFunc: the function RedactFunc builds removes every form Redact
// does (Redact is built on it), for many strings with one set of forms.
func TestRedactFunc(t *testing.T) {
	secrets := []string{"sk-test-0123456789abcdef", "line-one-of-it\nline-two-of-it"}
	red := RedactFunc(secrets)
	for _, s := range []string{
		"key " + secrets[0] + " and " + base64.StdEncoding.EncodeToString([]byte(secrets[0])),
		"pem " + secrets[1],
		"only line-two-of-it",
	} {
		got := red(s)
		if strings.Contains(got, "0123456789abcdef") || strings.Contains(got, "line-") || !strings.Contains(got, "[REDACTED]") {
			t.Errorf("RedactFunc(%q) = %q", s, got)
		}
	}
	if got := red("nothing here"); got != "nothing here" {
		t.Errorf("RedactFunc changed clean text: %q", got)
	}
}
