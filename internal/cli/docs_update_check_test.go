package cli

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestDocsUpdateCheckMatchesCode pins docs/gcp-setup.md's "Staying up to
// date" section against the real switches and field: the env var name, the
// local config key, and that localcfg.Config actually has the field the
// doc says turns the check off. A rename or removal of any of these
// without updating the doc fails this test (the same discipline
// docs_named_oauth_tokens_test.go and docs_project_layer_test.go already
// hold other docs to).
func TestDocsUpdateCheckMatchesCode(t *testing.T) {
	data, err := os.ReadFile("../../docs/gcp-setup.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	for _, want := range []string{
		"Staying up to date",
		"FUGARO_NO_UPDATE_CHECK=1",
		"`update_check: false`",
		"`cli-version`",
		"brew upgrade dimipaun/tap/fugaro",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/gcp-setup.md no longer says %q", want)
		}
	}
	if _, ok := reflect.TypeOf(localcfg.Config{}).FieldByName("UpdateCheck"); !ok {
		t.Fatal("localcfg.Config has no UpdateCheck field any more: docs/gcp-setup.md's \"Staying up to date\" section documents update_check: false against it")
	}
	if noUpdateCheckEnv != "FUGARO_NO_UPDATE_CHECK" {
		t.Fatalf("noUpdateCheckEnv = %q, want FUGARO_NO_UPDATE_CHECK (the doc names this exact variable)", noUpdateCheckEnv)
	}
}
