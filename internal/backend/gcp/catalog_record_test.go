package gcp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

// What the steps before the record step leave it, and Cloud Build's own build
// ID: the digest the build pushed, the base's digest as the record step strips
// the reference off it (the form imagecheck.Record's field holds), the build
// time the pin step wrote, and the value of $BUILD_ID.
const (
	recordBuildID = "8f3c1a2b-0000-4000-8000-000000000001"
	recordBuiltAt = "2026-11-03T09:00:00Z"
)

var (
	recordImageDigest = "sha256:" + strings.Repeat("c", 64)
	recordBaseDigest  = "sha256:" + strings.Repeat("d", 64)
)

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

// recordScript is the record step's script: what its bash is handed, without
// the -c that hands it over.
func recordScript(t *testing.T) string {
	t.Helper()
	b, err := gcp.CatalogBuildRequest("proj-1234", recordSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range b.Steps {
		if st.Id == catalogRecordStepID && st.Entrypoint == "bash" && len(st.Args) == 2 && st.Args[0] == "-c" {
			return st.Args[1]
		}
	}
	t.Fatalf("the request has no %s step with a bash -c script: %v", catalogRecordStepID, b.Steps)
	return ""
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
	script := recordScript(t)
	record := renderRecord(t, script, map[string]string{
		"ENTRY": spec.Entry, "COMMIT": spec.Definition.SourceCommit,
		"GENERATION": strconv.FormatInt(spec.Definition.Generation, 10),
		"SHA256":     spec.Definition.SHA256, "BASE": spec.Base, "IMAGE": spec.Image,
		"TAG": spec.Definition.SHA256, "BUILD_ID": recordBuildID,
		"image_digest": recordImageDigest, "base_digest": recordBaseDigest, "built_at": recordBuiltAt,
	})
	at, err := time.Parse(time.RFC3339, recordBuiltAt)
	if err != nil {
		t.Fatal(err)
	}
	var got imagecheck.CatalogRecordState
	strictDecode(t, record, &got)
	want := imagecheck.CatalogRecordState{
		Version: 1, Entry: spec.Entry, SourceCommit: spec.Definition.SourceCommit,
		DefinitionGeneration: spec.Definition.Generation, DefinitionSHA256: spec.Definition.SHA256,
		BaseRef: spec.Base, BaseDigest: recordBaseDigest, Image: spec.Image,
		ContentTag: spec.Definition.SHA256, ImageDigest: recordImageDigest,
		BuildID: recordBuildID, BuiltAt: at,
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

// fakeToken is what the fake gcloud below answers with: it holds a space and a
// ";" so that a token this step did not quote breaks the curl call the fake
// records rather than passing unnoticed.
const fakeToken = "not-a-real-token; and-not-a-command"

// catalogUploadPrefix is the JSON API's media upload for the runs bucket.
const catalogUploadPrefix = "https://storage.googleapis.com/upload/storage/v1/b/"

// recordUploadScript is the part of the record step's script that composes the
// record and uploads it: from the temporary file the record is written into,
// to the end of the script. The reads in front of it are the files the steps
// before it wrote, at /workspace paths a test cannot create, and everything
// the upload depends on — the record's own composition, the key's encoding,
// the bucket the URL names and the curl call — is inside what this returns.
//
// The step's $$ escapes are Cloud Build's, not the shell's, so they are
// resolved here the way Cloud Build resolves them: what bash then runs is what
// the step runs.
func recordUploadScript(t *testing.T) string {
	t.Helper()
	script := recordScript(t)
	const from = "record=$$(mktemp)"
	i := strings.Index(script, from)
	if i < 0 {
		t.Fatalf("the record step has no %q: %q", from, script)
	}
	upload := script[i:]
	if !strings.Contains(upload, catalogUploadPrefix) {
		t.Fatalf("the record step has no upload in the part this test runs: %q", upload)
	}
	if strings.Contains(upload, "${_") {
		t.Fatalf("the upload holds a substitution Cloud Build would rewrite: %q", upload)
	}
	// The script's own first line comes along, because a failed upload
	// failing the build is that line and the curl call together.
	head, _, ok := strings.Cut(script[:i], "\n")
	if !ok || head != "set -eu" {
		t.Fatalf("the record step's script does not open with set -eu: %q", script)
	}
	return head + "\n" + strings.ReplaceAll(upload, "$$", "$")
}

// recordUpload runs the part of the record step's script that uploads, with
// the two commands it may run faked, and returns the arguments its curl was
// called with and the script's combined output.
//
// The fakes are what make "one objects.create and nothing else" checkable:
// gcloud answers auth print-access-token and fails on any other call, so a
// `gcloud storage cp` or a listing in the step fails the run (gsutil or any
// other tool is not on the fake's PATH at all), and curl records its
// arguments, refuses to upload a file that is not there, and exits with
// curlExit.
func recordUpload(t *testing.T, key string, curlExit int) ([]string, string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("the record step runs bash, which this machine has not: %v", err)
	}
	spec := recordSpec(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("gcloud", `#!/bin/sh
case "$*" in
  "auth print-access-token"|"auth print-access-token --quiet") ;;
  *) echo "the record step ran gcloud $*" >&2; exit 1 ;;
esac
printf '%s' '`+fakeToken+`'
`)
	write("curl", `#!/bin/sh
printf '%s\n' "$@" > "$CURL_ARGS"
for a in "$@"; do
  case "$a" in
    @*) [ -f "${a#@}" ] || { echo "the upload's file ${a#@} is not there" >&2; exit 26; } ;;
  esac
done
exit "$CURL_EXIT"
`)
	argsFile := filepath.Join(dir, "curl-args")
	cmd := exec.Command(bash, "-c", recordUploadScript(t))
	cmd.Env = []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"CURL_ARGS=" + argsFile, "CURL_EXIT=" + strconv.Itoa(curlExit),
		"BUCKET=" + spec.Bucket, "KEY=" + key, "ENTRY=" + spec.Entry,
		"COMMIT=" + spec.Definition.SourceCommit,
		"GENERATION=" + strconv.FormatInt(spec.Definition.Generation, 10),
		"SHA256=" + spec.Definition.SHA256, "BASE=" + spec.Base, "IMAGE=" + spec.Image,
		"TAG=" + spec.Definition.SHA256, "BUILD_ID=" + recordBuildID,
		// What the reads cut out of the script would have left it: the step's
		// own variables, under the names its script reads them by.
		"image_digest=" + recordImageDigest, "base_digest=" + recordBaseDigest,
		"built_at=" + recordBuiltAt,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, string(out), fmt.Errorf("running the record step's script: %w", err)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		return nil, string(out), fmt.Errorf("the step's curl was never called: %w", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), string(out), nil
}

// recordUploadURL is the URL the step's curl was called with, which the fake
// makes the whole record of where the upload went.
func recordUploadURL(t *testing.T, args []string) string {
	t.Helper()
	for _, a := range args {
		if strings.HasPrefix(a, catalogUploadPrefix) {
			return a
		}
	}
	t.Fatalf("the step's curl was not called with an upload URL: %q", args)
	return ""
}

// recordUploadArg is the argument the step's curl was called with after flag,
// which the fake makes the whole record of the call it made.
func recordUploadArg(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("the step's curl was not called with %s: %q", flag, args)
	return ""
}

// TestCatalogRecordStepWritesOneObjectAndListsNothing: the record is written
// with the one permission the build account holds — storage.objects.create on
// the record's own name, which its objectUser condition on builds/<slug>/
// covers — so the step makes one objects.create call, the JSON API's media
// upload, and no call that lists the bucket, which that grant does not cover.
//
// The step's own script is run here (recordUpload), so this is a statement
// about what the step does and not about what it says: `gcloud storage cp`,
// which lists the destination before it copies, fails this test's fake gcloud
// exactly as it fails the build.
func TestCatalogRecordStepWritesOneObjectAndListsNothing(t *testing.T) {
	spec := recordSpec(t)
	key, err := gcp.CatalogRecordKey(spec.Slug, spec.Entry)
	if err != nil {
		t.Fatal(err)
	}
	script := recordScript(t)
	for _, lists := range []string{"gcloud storage ", "gsutil", " ls ", "objects list", "--recursive"} {
		if strings.Contains(script, lists) {
			t.Errorf("the record step carries %q, which lists the bucket: %q", lists, script)
		}
	}
	args, out, err := recordUpload(t, key, 0)
	if err != nil {
		t.Fatalf("the record step failed although its upload succeeds: %v\n%s", err, out)
	}
	if got, want := recordUploadURL(t, args), catalogUploadPrefix+"fugaro-runs-proj-1234/o?uploadType=media&name="+
		strings.ReplaceAll(key, "/", "%2F"); got != want {
		t.Errorf("the upload's URL is\n%s\nwant\n%s", got, want)
	}
	if got := recordUploadArg(t, args, "-X"); got != "POST" || !slices.Contains(args, "--fail") {
		t.Errorf("the upload is not a POST curl would fail on (%s, %q): %q", got, args, args)
	}
	// The token is read at run time from the build's own account and is the
	// argument of the header it is quoted in: a token with a space or a ";"
	// in it cannot split the call or add a header.
	token := "Authorization: Bearer " + fakeToken
	for i, a := range args {
		if a == "-H" && i+1 < len(args) && strings.HasPrefix(args[i+1], "Authorization:") && args[i+1] != token {
			t.Errorf("the upload's Authorization header is %q, want %q", args[i+1], token)
		}
	}
	// What goes up is the file this step composed, not a path that happens to
	// exist: the fake curl fails the run over a file that is not there.
	if data := recordUploadArg(t, args, "--data-binary"); !strings.HasPrefix(data, "@/") {
		t.Errorf("the upload sends %q, not a file", data)
	}
	// The step replaced the record before it and must keep doing so: the
	// record is per entry and per repository, and a rebuild of an entry has
	// to replace the record of the build it supersedes. A precondition would
	// fail every rebuild of an entry with 412 instead.
	if strings.Contains(strings.Join(args, " "), "ifGenerationMatch") {
		t.Errorf("the upload carries a generation precondition, so a rebuild of an entry cannot replace its record: %q", args)
	}
	if !strings.Contains(out, "recorded "+key) {
		t.Errorf("the step did not report the record it wrote:\n%s", out)
	}
}

// TestCatalogRecordStepEncodesTheKeyInTheUploadURL: the JSON API takes the
// object's name as a query parameter, so the step percent-encodes the key it
// uploads to byte by byte. The record's own key needs it for its "/" (the
// entry and slug grammars leave that the only character that does), and the
// encoding holds past those grammars, so a key that ever grew a "?" or an
// "&" could not add a parameter to the upload and point it somewhere else.
func TestCatalogRecordStepEncodesTheKeyInTheUploadURL(t *testing.T) {
	slug := recordSpec(t).Slug
	for _, tc := range []struct{ name, key, encoded string }{
		{
			"the record's key",
			"builds/" + slug + "/catalog/agent/image.json",
			"builds%2F" + slug + "%2Fcatalog%2Fagent%2Fimage.json",
		},
		{
			"a key with characters that need encoding",
			"builds/a b/catalog/x+y&z?w=v%r~s/catalog/~q/image.json",
			"builds%2Fa%20b%2Fcatalog%2Fx%2By%26z%3Fw%3Dv%25r~s%2Fcatalog%2F~q%2Fimage.json",
		},
	} {
		args, out, err := recordUpload(t, tc.key, 0)
		if err != nil {
			t.Errorf("%s: the record step failed: %v\n%s", tc.name, err, out)
			continue
		}
		want := catalogUploadPrefix + "fugaro-runs-proj-1234/o?uploadType=media&name=" + tc.encoded
		if got := recordUploadURL(t, args); got != want {
			t.Errorf("%s: the upload's URL is\n%s\nwant\n%s", tc.name, got, want)
		}
	}
}

// TestCatalogRecordStepFailsWhenTheUploadFails: the upload's failure is the
// step's (curl --fail under set -eu), so a build never reports a record it
// did not write — the whole of what the daily check reads is that object.
func TestCatalogRecordStepFailsWhenTheUploadFails(t *testing.T) {
	key, err := gcp.CatalogRecordKey(recordSpec(t).Slug, "agent")
	if err != nil {
		t.Fatal(err)
	}
	// 22 is curl's own "the server answered with an error".
	_, out, err := recordUpload(t, key, 22)
	if err == nil {
		t.Fatalf("the record step succeeded although its upload failed:\n%s", out)
	}
	if strings.Contains(out, "recorded ") {
		t.Fatalf("the step reported the record %s although the upload failed:\n%s", key, out)
	}
}
