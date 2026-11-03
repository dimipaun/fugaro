package gcp_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/task"
)

// This file is in package gcp_test, not package gcp with the rest of the
// catalog's tests: it decodes the record into imagecheck.CatalogRecordState,
// and internal/imagecheck imports this package (registry.go), so a test in
// the package itself could not import it back.
const catalogRecordStepID = "catalog-record"

// recordSpec is one catalog build of the entry "agent", as catalog_test.go's
// catalogBuildSpec builds it: the record step rendered below is the one that
// spec produces.
func recordSpec(t *testing.T) gcp.CatalogBuildSpec {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	image, err := gcp.SharedCatalogImage("us-east5", "proj-1234", "agent")
	if err != nil {
		t.Fatal(err)
	}
	return gcp.CatalogBuildSpec{
		Entry: "agent", Slug: slug,
		GitProvider: "bitbucket", RepoURL: "https://bitbucket.org/acme/app.git", BaseBranch: "main",
		GitSecretID: gcp.SecretID(slug, "bitbucket-token"), GitUser: "x-token-auth",
		Base:  "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc",
		Image: image,
		Definition: gcp.CatalogDefinition{
			SourceCommit: strings.Repeat("a", 40),
			Generation:   1712345678901234,
			SHA256:       strings.Repeat("b", 64),
		},
		ServiceAccount:  gcp.BuildServiceAccountID(slug) + "@proj-1234.iam.gserviceaccount.com",
		MachineType:     "E2_HIGHCPU_8",
		WorkflowSecrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}},
		Bucket:          "gs://fugaro-runs-proj-1234",
		Selftest:        []byte(`{"skip_verify":true,"tools":"critical"}`),
	}
}

// recordFormatRE takes the record step's printf apart: the format, then the
// arguments it is given, up to the redirect into the temporary file.
var recordFormatRE = regexp.MustCompile(`(?s)printf '([^']*)'\s*(.*?)\s*>\s*"\$\$record"`)

// renderRecord renders the record the record step composes, from the step's
// own format and arguments and the values its variables hold during a build:
// a value the test has none for, a verb the format has no argument for and
// an argument the format has no verb for all fail here rather than being
// papered over.
//
// Go's Sprintf renders the format the way printf(1) does because every verb
// in it is %s, which this checks: a format with another verb would have to
// be rendered by a shell to be tested honestly.
func renderRecord(t *testing.T, script string, values map[string]string) string {
	t.Helper()
	m := recordFormatRE.FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("the record step has no printf writing \"$$record\": %q", script)
	}
	format := m[1]
	if v := regexp.MustCompile(`%[^s]`).FindString(format); v != "" {
		t.Fatalf("the record's format carries the verb %q, which this test does not render the way printf(1) does: %q", v, format)
	}
	var args []any
	// The arguments are one per line, each ending with a backslash.
	for _, word := range strings.Fields(strings.ReplaceAll(m[2], "\\\n", " ")) {
		name := strings.TrimPrefix(strings.TrimPrefix(strings.Trim(word, `"`), "$"), "$")
		v, ok := values[name]
		if !ok {
			t.Fatalf("the record step passes %s, which this test has no value for", word)
		}
		args = append(args, v)
	}
	record := fmt.Sprintf(format, args...)
	if strings.Contains(record, "%!") {
		t.Fatalf("the record's format and its arguments do not line up: %q", record)
	}
	return record
}

// TestCatalogRecordDecodesStrictly: the record step composes its JSON with
// printf and no escaping, on the claim that every value it carries is
// constrained. That claim has to hold against the reader, not only against a
// grep for field names: this renders the step's own printf with the values a
// build has and decodes the result the way the check does, strictly
// (DisallowUnknownFields), into imagecheck.CatalogRecordState.
//
// Every field the check reads is compared, so a field the record stops
// writing fails here as a zero value; the format and the arguments both come
// from the request, so a value that needs escaping fails here too; and the
// last check proves the decode is strict, by handing it a record with a key
// the check does not know.
func TestCatalogRecordDecodesStrictly(t *testing.T) {
	spec := recordSpec(t)
	b, err := gcp.CatalogBuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, st := range b.Steps {
		if st.Id == catalogRecordStepID {
			script = strings.Join(st.Args, "\n")
		}
	}
	if script == "" {
		t.Fatalf("the request has no %s step: %v", catalogRecordStepID, b.Steps)
	}
	const (
		builtAt = "2026-11-03T09:00:00Z"
		buildID = "8f3c1a2b-0000-4000-8000-000000000001"
	)
	// The step's own variables come from the files the steps before it wrote
	// (the pushed digest, the base's digest and the pin's build time).
	imageDigest, baseDigest := "sha256:"+strings.Repeat("c", 64), "sha256:"+strings.Repeat("d", 64)
	record := renderRecord(t, script, map[string]string{
		"ENTRY": spec.Entry, "COMMIT": spec.Definition.SourceCommit,
		"GENERATION": strconv.FormatInt(spec.Definition.Generation, 10),
		"SHA256":     spec.Definition.SHA256, "BASE": spec.Base, "IMAGE": spec.Image,
		"TAG": spec.Definition.SHA256, "BUILD_ID": buildID,
		"image_digest": imageDigest, "base_digest": baseDigest, "built_at": builtAt,
	})
	at, err := time.Parse(time.RFC3339, builtAt)
	if err != nil {
		t.Fatal(err)
	}
	var got imagecheck.CatalogRecordState
	strictDecode(t, record, &got)
	want := imagecheck.CatalogRecordState{
		Version: 1, Entry: spec.Entry, SourceCommit: spec.Definition.SourceCommit,
		DefinitionGeneration: spec.Definition.Generation, DefinitionSHA256: spec.Definition.SHA256,
		BaseRef: spec.Base, BaseDigest: baseDigest, Image: spec.Image,
		ContentTag: spec.Definition.SHA256, ImageDigest: imageDigest,
		BuildID: buildID, BuiltAt: at,
	}
	if got != want {
		t.Fatalf("the record the step writes decodes to\n%+v\nwant\n%+v\nfrom %s", got, want, record)
	}
	// The decode above is strict: a key the check does not know is refused,
	// which is what makes "every field present" a claim about this record
	// rather than about whatever happens to parse.
	var withExtra imagecheck.CatalogRecordState
	err = strictDecodeErr(strings.Replace(record, "}", `,"a_field_the_check_does_not_know":1}`, 1), &withExtra)
	if err == nil || !strings.Contains(err.Error(), "a_field_the_check_does_not_know") {
		t.Fatalf("a record with an unknown field decoded without an error (%v)", err)
	}
}

// strictDecode decodes one record the way the check does and fails the test
// when it does not decode.
func strictDecode(t *testing.T, record string, st *imagecheck.CatalogRecordState) {
	t.Helper()
	if err := strictDecodeErr(record, st); err != nil {
		t.Fatalf("the record the step writes does not decode into imagecheck.CatalogRecordState: %v\n%s", err, record)
	}
}

// strictDecodeErr is the check's own decode: every field it knows, and no
// other.
func strictDecodeErr(record string, st *imagecheck.CatalogRecordState) error {
	dec := json.NewDecoder(strings.NewReader(record))
	dec.DisallowUnknownFields()
	return dec.Decode(st)
}
