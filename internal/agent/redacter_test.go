package agent

import (
	"encoding/base64"
	"testing"
)

func TestRedacterMatchesRedact(t *testing.T) {
	secrets := []string{"sk-test-0123456789abcdef", "line-one-of-it\nline-two-of-it"}
	red := Redacter(secrets)
	for _, s := range []string{
		"key " + secrets[0] + " and " + base64.StdEncoding.EncodeToString([]byte(secrets[0])),
		"pem " + secrets[1],
		"nothing here",
	} {
		if got, want := red(s), Redact(s, secrets); got != want {
			t.Errorf("Redacter(%q) = %q, Redact = %q", s, got, want)
		}
	}
}
