package redact

import "testing"

func TestZZProbe(t *testing.T) {
	tok := "QWERTYUIOPASDFGHJKLZXCVBNM"
	for _, c := range []string{
		`Authorization: Token token='` + tok + `'`,
		`Authorization: Token token= "` + tok + `"`,
		`Authorization: Token token = "` + tok + `"`,
		`Authorization: Token token ="` + tok + `"`,
		`Authorization: OAuth oauth_token='a', oauth_signature='` + tok + `'`,
		`{"message":"Authorization: Token token=\"` + tok + `\"","step":"x"}`,
	} {
		t.Logf("in : %s\nout: %s", c, Text(c, nil))
	}
}
