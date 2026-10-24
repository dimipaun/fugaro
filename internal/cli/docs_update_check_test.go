package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestDocsUpdateCheckMatchesCode pins the switches the update nag answers to
// against the real ones: the env var name, the local config key, and that
// localcfg.Config actually has the field the doc says turns the check off. The
// setup guide's "Staying up to date" section carried this until that page became
// the short setup path (this review's finding: it carries the fresh-setup path
// only); the switches are stated where the check itself is explained, in
// docs/release.md's releases-API section, and are pinned there now.
func TestDocsUpdateCheckMatchesCode(t *testing.T) {
	const path = "docs/release.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		"FUGARO_NO_UPDATE_CHECK=1",
		"`update_check: false`",
		"`cli-version`",
		"brew upgrade dimipaun/tap/fugaro",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer says %q", path, want)
		}
	}
	if _, ok := reflect.TypeOf(localcfg.Config{}).FieldByName("UpdateCheck"); !ok {
		t.Fatalf("localcfg.Config has no UpdateCheck field any more: %s documents update_check: false against it", path)
	}
	if noUpdateCheckEnv != "FUGARO_NO_UPDATE_CHECK" {
		t.Fatalf("noUpdateCheckEnv = %q, want FUGARO_NO_UPDATE_CHECK (the doc names this exact variable)", noUpdateCheckEnv)
	}
}
