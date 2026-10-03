package infra

import (
	"path/filepath"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

func TestPublicSignUp(t *testing.T) {
	got, err := PublicSignUp([]byte(`{"signIn":{"email":{"enabled":true},"anonymous":{"enabled":false}},"client":{"permissions":{"disabledUserSignup":true}}}`))
	if err != nil || len(got) != 1 || got[0] != "signIn.email.enabled" {
		t.Errorf("got %v, %v", got, err)
	}
	got, _ = PublicSignUp([]byte(`{"client":{"apiKey":"x"}}`))
	if len(got) != 0 {
		t.Errorf("an empty configuration is public: %v", got)
	}
}

// The firebase module must declare no Identity Platform configuration (init
// ensures it over REST; the resource doesn't adopt) and no sign-in provider.
func TestFirebaseModuleDeclaresNoSignIn(t *testing.T) {
	entries, err := terraform.FS.ReadDir("gcp/modules/firebase")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".tf" {
			continue
		}
		b, err := terraform.FS.ReadFile("gcp/modules/firebase/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "google_identity_platform") {
				t.Errorf("%s declares %q: no Identity Platform resource, no sign-in provider", e.Name(), strings.TrimSpace(line))
			}
		}
	}
}
