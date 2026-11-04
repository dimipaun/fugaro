package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/option"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
)

// The audit record's tests (M6, D1014, D1025): what one record holds, where it
// is written, and that a write which failed fails the command.
//
// Every oracle here is written by hand rather than read off the type under
// test. The digest is the test's own sha256 of the bytes it handed in — a
// record whose plan_digest is the digest of the bytes the gate read is the
// whole of D1022, and a test that asked this package's own helper would agree
// with a bug in that helper. The generation fields are checked against the
// object's own bytes, so "empty rather than a zero" is a fact about the JSON a
// reader sees and not about a Go field.

// auditBucket is the runs bucket of the installation the records below are
// about: the name the installation module gives it (fugaro-runs-<gcp
// project>), spelled here rather than built from the read's own function.
const auditBucket = "fugaro-runs-proj-1234"

// auditStorage is the storage client the record is written with, over the
// runs bucket the installation's own account writes.
func auditStorage(t *testing.T) (*storage.Service, *gcpfake.GCS) {
	t.Helper()
	f := gcpfake.NewGCS(t)
	f.AddBucket(auditBucket, 1234, nil)
	s, err := storage.NewService(context.Background(), option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/storage/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

// auditApply is the command's own facts: one set, so every row below differs
// from its neighbour in the one field it is about.
func auditApply() AuditFacts {
	return AuditFacts{
		Command:          "fugaro config publish",
		Argv:             []string{"config", "publish", "fugaro.yaml"},
		Project:          "aurora",
		GCPProject:       "proj-1234",
		Identity:         "user:operator@example.invalid",
		AgentMarker:      "CLAUDECODE",
		MarkerVersion:    inframanaged.MarkerVersionBuilds,
		MarkerGeneration: 17,
		At:               time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC),
	}
}

// auditKeys is every object under the family's prefix, in the listing's own
// order.
func auditKeys(t *testing.T, s *storage.Service) []string {
	t.Helper()
	list, err := s.Objects.List(auditBucket).Prefix(AuditPrefix).Do()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(list.Items))
	for _, o := range list.Items {
		out = append(out, o.Name)
	}
	return out
}

// auditBytes is one object's own bytes: the JSON a reader outside this build
// sees, which is where "the field is empty rather than a zero" is decided.
func auditBytes(t *testing.T, s *storage.Service, key string) []byte {
	t.Helper()
	resp, err := s.Objects.Get(auditBucket, key).Download()
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// auditRoundTrip writes the record and parses what is there back through the
// reader M7's check uses, so every test below reads what a surface would print
// rather than what this test handed in.
func auditRoundTrip(t *testing.T, s *storage.Service, rec AuditRecord) (AuditRecord, string, []byte) {
	t.Helper()
	key, err := WriteAudit(context.Background(), s, auditBucket, rec)
	if err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	data := auditBytes(t, s, key)
	got, err := ParseAuditRecord(data)
	if err != nil {
		t.Fatalf("parsing %s back: %v", key, err)
	}
	return got, key, data
}

// digest is the oracle for plan_digest: the sha256 of the bytes, hex-encoded,
// computed here rather than by the code under test.
func digest(doc []byte) string {
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:])
}

// The red step: a gate refusal leaves a record too (D1025). It fails today
// because nothing in this package writes one, and it is the record an owner
// most wants — an unattended refusal that leaves no trail is indistinguishable
// from a command that never ran.
func TestManagedAuditRefusalIsStillRecorded(t *testing.T) {
	s, _ := auditStorage(t)
	doc := []byte(`{"version":1,"keys":{}}`)
	const rule = "the layer names credentials.api_key, which is a reserved key"
	rec := AuditRecordFor(auditApply(), ConvergeRefused(doc, initflow.YesOrTyped, []string{rule}))

	got, key, _ := auditRoundTrip(t, s, rec)
	if got.Verdict != AuditRefused {
		t.Errorf("the record's verdict is %q, want %q", got.Verdict, AuditRefused)
	}
	if !slices.Contains(got.Rules, rule) {
		t.Errorf("the record's rules are %q, and the rule that refused is not among them", got.Rules)
	}
	if keys := auditKeys(t, s); len(keys) != 1 || keys[0] != key {
		t.Errorf("the family holds %q, want exactly the one record %q", keys, key)
	}
}

// The digest is of the bytes the gate READ (D1022), not of a re-render and not
// of the command's arguments: the judged document and the applied one are
// provably one artifact, and one byte of difference is a different record.
func TestManagedAuditPlanDigestIsTheJudgedBytes(t *testing.T) {
	s, _ := auditStorage(t)
	doc := []byte(`{"version":1,"keys":{"rigor":"standard"}}`)
	rec := AuditRecordFor(auditApply(), ConvergeAdmitted(doc, initflow.YesOrTyped, []string{"every key is in the registry"}))

	got, _, _ := auditRoundTrip(t, s, rec)
	if got.PlanDigest != digest(doc) {
		t.Errorf("the record's plan_digest is %q, want the sha256 of the bytes the gate read (%q)", got.PlanDigest, digest(doc))
	}

	// One character apart is a different document, and so a different digest: a
	// record that could not tell the two apart would be a record an owner
	// cannot check the applied plan against.
	other := []byte(`{"version":1,"keys":{"rigor":"strict"}}`)
	second := AuditRecordFor(auditApply(), ConvergeAdmitted(other, initflow.YesOrTyped, nil))
	if second.PlanDigest == got.PlanDigest {
		t.Errorf("two documents differing by one byte carry the same plan_digest %q", got.PlanDigest)
	}
	if second.PlanDigest != digest(other) {
		t.Errorf("the second record's plan_digest is %q, want %q", second.PlanDigest, digest(other))
	}
}

// The prefix is the whole of why this family outlives a quarter (spec §6): the
// runs bucket's owner-only area, where a job account's grants are conditioned
// to a prefix BELOW fugaro/ and so cannot reach it. A record filed under runs/
// would be deleted at 90 days and would be writable by the account this design
// keeps out of the write path.
func TestManagedAuditIsWrittenUnderTheOperatorPrefix(t *testing.T) {
	s, _ := auditStorage(t)
	rec := AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	_, key, _ := auditRoundTrip(t, s, rec)

	if !strings.HasPrefix(key, "fugaro/") {
		t.Fatalf("the record is at %q, which is not under the fugaro/ prefix only operators may write", key)
	}
	for _, p := range gcp.JobBucketPrefixes {
		if strings.HasPrefix(key, p+"/") {
			t.Errorf("the record is at %q, under the %s/ prefix a run's job account writes", key, p)
		}
	}
	if !strings.HasPrefix(key, AuditPrefix+AuditDate(rec.At)+"/") || !strings.HasSuffix(key, ".json") {
		t.Errorf("the record is at %q, want %s<utc date>/<id>.json", key, AuditPrefix)
	}
	if !strings.HasPrefix(inframanaged.MarkerObject, "fugaro/") {
		t.Errorf("the marker is at %q, and the audit family is meant to sit in the same owner-only area", inframanaged.MarkerObject)
	}
}

// A write that failed fails the command (D1014): the record is a control and
// not a courtesy, so an apply that could not leave a trail is an apply that
// did not happen. The fake refuses every write under fugaro/, as a bucket
// whose grant is conditioned elsewhere answers.
func TestManagedAuditWriteFailureFailsTheCommand(t *testing.T) {
	s, f := auditStorage(t)
	f.DenyWrites(auditBucket, "fugaro/")
	rec := AuditRecordFor(auditApply(), ConvergeRefused([]byte("{}"), initflow.YesOrTyped, []string{"the layer widens a scope rule"}))
	if _, err := WriteAudit(context.Background(), s, auditBucket, rec); err == nil {
		t.Fatal("a refused write was reported as written")
	}
	if keys := auditKeys(t, s); len(keys) != 0 {
		t.Errorf("the family holds %q after a refused write, want nothing", keys)
	}
}

// Which version of the owner's list admitted a delete is a field with the same
// weight as the plan digest: without it an owner cannot audit a removal
// against the file they wrote.
func TestManagedAuditRecordsTheDeleteListGeneration(t *testing.T) {
	s, _ := auditStorage(t)
	apply := auditApply()
	apply.DeleteListGeneration = 42
	rec := AuditRecordFor(apply, ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, []string{"the address is one the owner's list approves"}))

	got, _, data := auditRoundTrip(t, s, rec)
	if got.DeleteListGeneration != "42" {
		t.Errorf("the record's delete_list_generation is %q, want %q", got.DeleteListGeneration, "42")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["delete_list_generation"]; !ok {
		t.Errorf("the record's bytes carry no delete_list_generation: %s", data)
	}
}

// The negative half, and the reason the field is a string: an apply that judged
// no removal carries an EMPTY field rather than a zero, because a zero reads as
// "generation 0 of the owner's list was in force" and no such list exists.
func TestManagedAuditRecordsNoDeleteListGenerationWhenNoRemovalWasJudged(t *testing.T) {
	s, _ := auditStorage(t)
	rec := AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, []string{"the plan creates and updates only"}))

	got, _, data := auditRoundTrip(t, s, rec)
	if got.DeleteListGeneration != "" {
		t.Errorf("the record's delete_list_generation is %q, want it empty", got.DeleteListGeneration)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw["delete_list_generation"]; ok {
		t.Errorf("the record's bytes carry delete_list_generation %s, want the field absent", v)
	}
}

// The record is versioned like the marker, and an unknown version is refused
// rather than guessed: a later build's record read as this one's shape is the
// mutation the version exists to stop.
func TestParseAuditRecordRefusesAnUnknownVersion(t *testing.T) {
	s, _ := auditStorage(t)
	rec := AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	_, key, data := auditRoundTrip(t, s, rec)

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["version"] = AuditVersion + 1
	later, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAuditRecord(later); err == nil {
		t.Errorf("a version %d record at %s parsed as this build's", AuditVersion+1, key)
	}
	// And the shape is strict: a field this build does not write is refused
	// rather than ignored, so a record cannot carry a meaning nothing reads.
	m["version"] = AuditVersion
	m["tamper_proof"] = true
	extra, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseAuditRecord(extra); err == nil {
		t.Error("a record with a field this build does not write parsed")
	}
}

// A generation is a positive count in decimal and nothing else: a field that
// carried a word, a negative number or a zero would be text a surface prints
// and an owner cannot compare against anything.
func TestParseAuditRecordRefusesAGenerationThatIsNotOne(t *testing.T) {
	s, _ := auditStorage(t)
	rec := AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	_, _, data := auditRoundTrip(t, s, rec)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{"-1", "0", "17‮gnihtemos", float64(17), " 17"} {
		m["marker_generation"] = bad
		body, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseAuditRecord(body); err == nil {
			t.Errorf("a marker_generation of %#v parsed as a generation", bad)
		}
	}
}

// The reader answers one UTC date's records, parsed by the one parser, in the
// listing's own order: this is the read M7's doctor check and the report's
// section use, so what a surface prints and what the object holds cannot drift.
func TestListAuditRecordsReadsOneDate(t *testing.T) {
	s, _ := auditStorage(t)
	day := auditApply()
	day.At = time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)
	other := auditApply()
	other.At = time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	other.Command = "fugaro update"
	for _, a := range []AuditFacts{day, day, other} {
		if _, err := WriteAudit(context.Background(), s, auditBucket, AuditRecordFor(a, ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListAuditRecords(context.Background(), s, auditBucket, "2026-11-03")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("the read answered %d records for the day, want 2", len(got))
	}
	for _, r := range got {
		if !r.At.Equal(day.At) {
			t.Errorf("the read answered a record of %s, want only the day's", r.At)
		}
		if r.Command != day.Command {
			t.Errorf("the read answered the command %q, want %q", r.Command, day.Command)
		}
	}
	// A day nothing was filed under is no records and not an error: a check
	// that failed for a project which never ran a managed apply would fail
	// every ordinary installation.
	empty, err := ListAuditRecords(context.Background(), s, auditBucket, "2026-10-01")
	if err != nil || len(empty) != 0 {
		t.Errorf("an empty day answered %d records and %v, want none and no error", len(empty), err)
	}
}

// A record this build did not write is refused whole rather than partly
// believed: an object that cannot be parsed is an error at the read, so no
// surface prints a field out of an object nobody can vouch for.
func TestListAuditRecordsRefusesAnObjectThatIsNotARecord(t *testing.T) {
	s, f := auditStorage(t)
	f.Put(auditBucket, AuditPrefix+"2026-11-03/not-a-record.json", []byte(`{"version":1,"command":"fugaro update"}`))
	if _, err := ListAuditRecords(context.Background(), s, auditBucket, "2026-11-03"); err == nil {
		t.Fatal("an object that is not a record was read as one")
	}
}

// The writer refuses a record it could not read back, so no object this build
// cannot parse is ever written: an empty command, no project, a digest that is
// not one and a zero version are each a refusal and not a record.
func TestWriteAuditRefusesARecordThisBuildCouldNotRead(t *testing.T) {
	s, _ := auditStorage(t)
	good := AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))
	for _, tc := range []struct {
		what string
		mut  func(*AuditRecord)
	}{
		{"no command", func(r *AuditRecord) { r.Command = "" }},
		{"no project", func(r *AuditRecord) { r.Project = "" }},
		{"no GCP project", func(r *AuditRecord) { r.GCPProject = "" }},
		{"no digest", func(r *AuditRecord) { r.PlanDigest = "" }},
		{"a digest that is not one", func(r *AuditRecord) { r.PlanDigest = "not a digest" }},
		{"a verdict that is neither", func(r *AuditRecord) { r.Verdict = "maybe" }},
		{"no time", func(r *AuditRecord) { r.At = time.Time{} }},
		{"a version this build does not write", func(r *AuditRecord) { r.Version = AuditVersion + 1 }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := good
			tc.mut(&rec)
			if _, err := WriteAudit(context.Background(), s, auditBucket, rec); err == nil {
				t.Fatal("the writer wrote a record it could not read back")
			}
		})
	}
	if keys := auditKeys(t, s); len(keys) != 0 {
		t.Errorf("the family holds %q after refused writes, want nothing", keys)
	}
}

// A record whose object cannot be written where the caller said is an error
// and never a silent success, whatever the cloud answered with: a 5xx is the
// same answer as a 403 here, because a trail that was not left is not a trail.
func TestWriteAuditReportsAFailureOfTheWrite(t *testing.T) {
	s, f := auditStorage(t)
	f.FailObjectStoresAfterWrite(1)
	rec := AuditRecordFor(auditApply(), ConvergeRefused([]byte("{}"), initflow.YesOrTyped, []string{"the plan removes a bucket"}))
	if _, err := WriteAudit(context.Background(), s, auditBucket, rec); err == nil {
		t.Fatal("a write the cloud answered with an error was reported as written")
	}
}

// The reader's own transport failure is an error and never "no records": a
// check that read nothing must not report a project as having no trail.
func TestListAuditRecordsReportsAReadThatFailed(t *testing.T) {
	s, f := auditStorage(t)
	f.FailListings(auditBucket)
	if _, err := ListAuditRecords(context.Background(), s, auditBucket, "2026-11-03"); err == nil {
		t.Fatal("a listing that failed was read as a day with no records")
	}
	if _, err := ListAuditRecords(context.Background(), nil, auditBucket, "2026-11-03"); !errors.Is(err, ErrNoAuditReader) {
		t.Errorf("a read with no storage client answered %v, want ErrNoAuditReader", err)
	}
	if _, err := WriteAudit(context.Background(), nil, auditBucket, AuditRecordFor(auditApply(), ConvergeAdmitted([]byte("{}"), initflow.YesOrTyped, nil))); !errors.Is(err, ErrNoAuditReader) {
		t.Errorf("a write with no storage client answered %v, want ErrNoAuditReader", err)
	}
}
