package identitywire_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/identitywire"
)

// fieldsOf answers the JSON spelling of every field of a body's type, in the
// order the type declares them.
func fieldsOf(v any) []string {
	typ := reflect.TypeOf(v)
	out := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		out = append(out, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	return out
}

// TestTheTicketNamesAJobAndTheClaimCarriesAnAttestation is C1's first
// acceptance: the batch half of the wire is FOUR fields and no more — the job
// the ticket ask names and the attestation the claim carries, and the job and
// the id the record holds (R1, R18, R19) — and each of the four is left out of
// its body when it is empty, so a Cloud Run launch's ask, claim and record are
// byte for byte the release behind's.
//
// The field lists are pinned whole, and that is the point of the test: the
// acceptance is "these are the only wire change", and a fifth field on any of
// the three types is a change to a contract two sides read, which is a
// deliberate edit to this list and never an accident.
func TestTheTicketNamesAJobAndTheClaimCarriesAnAttestation(t *testing.T) {
	const (
		job = "projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271"
		att = "h.p.s"
	)

	for _, tc := range []struct {
		name string
		zero any
		want []string
	}{
		{"TicketRequest", identitywire.TicketRequest{}, []string{
			"slug", "run", "account", "launcher", "branch", "pull_requests",
			"context_repos", "max_runtime_seconds", "attempt", "job",
		}},
		{"ClaimRequest", identitywire.ClaimRequest{}, []string{"ticket", "nonce", "attestation"}},
		{"Record", identitywire.Record{}, []string{
			"caller", "class", "at", "claims", "expected_account", "ticket_hash",
			"claimed", "attempt", "job", "job_id",
		}},
	} {
		if got := fieldsOf(tc.zero); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s has the fields %v, want %v: the batch half of this contract is the job the ticket ask names, the attestation the claim carries, and the record's job and id, and nothing else", tc.name, got, tc.want)
		}
	}

	// What the two bodies carry of it, spelled as the wire spells them.
	ask, err := json.Marshal(identitywire.TicketRequest{
		Slug: "acme-sandbox", Run: "fb-20261120-041919-8271",
		Account: "fugaro-job@acme.iam.gserviceaccount.com", Launcher: "dimi@example.com",
		MaxRuntimeSeconds: 3600, Job: identitywire.JobName(job),
	})
	if err != nil {
		t.Fatalf("a ticket ask does not encode: %v", err)
	}
	if !strings.Contains(string(ask), `"job":"`+job+`"`) {
		t.Errorf("a ticket ask that names a job encodes as\n%s\nand the job the launcher is about to create is not on it", ask)
	}
	claim, err := json.Marshal(identitywire.ClaimRequest{Ticket: "t-1", Nonce: "n-1", Attestation: identitywire.Attestation(att)})
	if err != nil {
		t.Fatalf("a claim does not encode: %v", err)
	}
	if !strings.Contains(string(claim), `"attestation":"`+att+`"`) {
		t.Errorf("a claim that carries an attestation encodes as\n%s\nand the VM's own token is not on it", claim)
	}

	// The two bodies of a Cloud Run launch: no field, no bytes.
	cloudAsk, err := json.Marshal(identitywire.TicketRequest{
		Slug: "acme-sandbox", Run: "fb-20261120-041919-8271",
		Account: "fugaro-job@acme.iam.gserviceaccount.com", Launcher: "dimi@example.com",
		MaxRuntimeSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("a Cloud Run ticket ask does not encode: %v", err)
	}
	if strings.Contains(string(cloudAsk), `"job"`) {
		t.Errorf("a Cloud Run ticket ask encodes a job field:\n%s\nits ask must be the release behind's, byte for byte (R18)", cloudAsk)
	}
	cloudClaim, err := json.Marshal(identitywire.ClaimRequest{Ticket: "t-1", Nonce: "n-1"})
	if err != nil {
		t.Fatalf("a Cloud Run claim does not encode: %v", err)
	}
	if strings.Contains(string(cloudClaim), `"attestation"`) {
		t.Errorf("a Cloud Run claim encodes an attestation field:\n%s\nits claim must be the release behind's, byte for byte (R18)", cloudClaim)
	}

	// The record: a Batch launch's names a job, a Cloud Run launch's names
	// neither a job nor an id, and a Batch launch's carries no id until the
	// registration writes one (R19).
	batch := identitywire.Record{Job: identitywire.JobName(job)}
	if !batch.NamesAJob() {
		t.Error("a record that names a job does not report one: the field is what decides how the run may claim (D3114)")
	}
	cloud := identitywire.Record{}
	if cloud.NamesAJob() {
		t.Error("a record that names no job reports one: a Cloud Run run's record must read as the release behind's")
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("a record does not encode: %v", err)
	}
	if !strings.Contains(string(raw), `"job":"`+job+`"`) || strings.Contains(string(raw), `"job_id"`) {
		t.Errorf("a record that names a job and no id encodes as\n%s\nwant the job it names and no id field at all (the registration writes the id, R19)", raw)
	}
	if raw, err := json.Marshal(cloud); err != nil {
		t.Fatalf("a Cloud Run record does not encode: %v", err)
	} else if strings.Contains(string(raw), `"job"`) || strings.Contains(string(raw), `"job_id"`) {
		t.Errorf("a Cloud Run run's record encodes a job field:\n%s\nthe record must be the release behind's, byte for byte (R18)", raw)
	}

	// The attestation's shape, and its two refusals: what is not three
	// dot-separated base64url segments is no identity token of the metadata
	// server's (U2). It is a spelling check and not a proof — the test says so
	// by accepting a token-shaped value that no VM ever issued.
	for _, tc := range []struct {
		att  identitywire.Attestation
		want bool
	}{
		{"h.p.s", true},
		{"aGVhZGVy.cGF5bG9hZA.c2lnbmF0dXJl", true},
		{"", false},
		{"h.p", false},
		{"h.p.s.x", false},
		{"h..s", false},
		{".p.s", false},
		{"h.p.", false},
		{"h+p+s", false},
		{"h.p.s ", false},
	} {
		if got := tc.att.WellFormed(); got != tc.want {
			t.Errorf("Attestation(%q).WellFormed() is %v, want %v", tc.att, got, tc.want)
		}
	}
}

// TestABodyNamingAFieldTheServiceDoesNotKnowIsRefusedByName is C1's second
// acceptance, and it is asked twice over because the two sides of a bridge read
// different types for one field (F37).
//
// Every body of the wire is asked for the general rule (a body naming a field
// its type does not have is refused, and the reader's own error names the
// field). Then the one case the plan is about is asked with the release
// behind's own type: a claim carrying this release's attestation, read by a
// service whose ClaimRequest has no such field, is refused BY NAME. That is
// what makes the bridge's second trigger a fact and not a hope — the runner
// sends the field, the older service says which field it does not know, and the
// runner of this release reads that answer rather than serving a claim under a
// value the service ignored.
func TestABodyNamingAFieldTheServiceDoesNotKnowIsRefusedByName(t *testing.T) {
	for _, b := range bodies() {
		t.Run(b.name, func(t *testing.T) {
			named := withField(b.valid, `"a_field_no_type_here_has":1`)
			err := identitywire.DecodeStrict([]byte(named), reflect.New(reflect.TypeOf(b.zero)).Interface())
			if err == nil {
				t.Fatalf("%s decoded a body naming a field its type does not have:\n%s", b.name, named)
			}
			if !strings.Contains(err.Error(), "a_field_no_type_here_has") {
				t.Errorf("%s refused the unknown field without naming it: %v\nthe refusal is read by the other side of a bridge, and a refusal that does not name the field is one it cannot act on", b.name, err)
			}
		})
	}

	// The release behind's claim: this release's two fields and nothing.
	type claimBehind struct {
		Ticket string `json:"ticket"`
		Nonce  string `json:"nonce"`
	}
	claim := `{"ticket":"t-1","nonce":"n-1","attestation":"h.p.s"}`
	into := claimBehind{}
	err := identitywire.DecodeStrict([]byte(claim), &into)
	if err == nil {
		t.Fatalf("a service of the release behind decoded a claim carrying an attestation:\n%s\nits own ClaimRequest has no such field, and a body it ignores is a claim served on the account check alone (D3114)", claim)
	}
	if !strings.Contains(err.Error(), "attestation") {
		t.Errorf("the release-behind refusal does not name the field: %v", err)
	}
	if into != (claimBehind{Ticket: "t-1", Nonce: "n-1"}) {
		t.Errorf("the refused claim left %+v behind: a refused body is not half-read", into)
	}
}

// TestTheRegistrationRouteNamesTheJobAndItsId is C1's third acceptance: the
// registration is a route of this contract (BR17, R19) — RouteFor names it, the
// table admits the classes that may launch a run, and its body carries the run
// whose record is written, the job the ticket ask named and the id the platform
// gave that job.
//
// It says nothing about the service's door: mounting the path and answering a
// caller there is BR17's, and this contract's own word is the table.
func TestTheRegistrationRouteNamesTheJobAndItsId(t *testing.T) {
	if identitywire.JobPath != "/v1/identity/job" {
		t.Errorf("JobPath is %q, want the registration's one path", identitywire.JobPath)
	}
	rt, found := identitywire.RouteFor(identitywire.JobPath)
	if !found {
		t.Fatalf("RouteFor(%q) answers no route: the registration is a route of this wire, and a path the table does not carry is a path no reader of this contract can name", identitywire.JobPath)
	}
	want := []identitywire.CallerClass{
		identitywire.ClassPerson, identitywire.ClassServiceAccount, identitywire.ClassOrchestrator,
	}
	if !reflect.DeepEqual(rt.Admits, want) {
		t.Errorf("the registration admits %v, want %v: the registration is the ticket ask's second half, and the classes that may be issued a run are the classes that may name the job it launched (D3123, R19, R20)", rt.Admits, want)
	}
	if rt.OperatorsOnly {
		t.Error("the registration admits the roster's operators alone: only the probe may (R2)")
	}

	const (
		job = "projects/acme-sandbox/locations/europe-west1/jobs/fb-20261120-041919-8271"
		// The slug's own shape is task.Slug's — a readable part and the 16 hex of
		// the repository's hash — because the record's path is built from it.
		slug = "acme-sandbox-0123456789abcdef"
		run  = "fb-20261120-041919-8271"
	)
	req := identitywire.JobRequest{Slug: slug, Run: run, Job: identitywire.JobName(job), JobID: "job-8271"}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("a registration does not encode: %v", err)
	}
	var back identitywire.JobRequest
	if err := identitywire.DecodeStrict(raw, &back); err != nil {
		t.Fatalf("the registration's own encoding does not decode: %v\n%s", err, raw)
	}
	if back != req {
		t.Errorf("the registration decodes as %+v, want %+v", back, req)
	}

	// The record it writes is keyed by the two names the body carries, and a
	// pair the record's own spelling could not carry is refused rather than
	// escaped onto another node.
	for _, tc := range []struct {
		slug, run string
		wantErr   bool
	}{
		{slug, run, false},
		{"", "fb-1", true},
		{slug, "", true},
		{slug, "a/b", true},
		{slug + "/x", run, true},
		{"acme.sandbox-0123456789abcdef", run, true},
		{"acme-sandbox", run, true},
		{"NotASlug-0123456789abcdef", run, true},
	} {
		gotSlug, gotRun, err := identitywire.JobRequest{Slug: tc.slug, Run: tc.run}.Record()
		if tc.wantErr {
			if err == nil {
				t.Errorf("Record() accepted the pair %q/%q as %q/%q: the record's path is built from these two names, so a name it cannot carry is a second node for one run", tc.slug, tc.run, gotSlug, gotRun)
			}
			continue
		}
		if err != nil {
			t.Errorf("Record() refused %q/%q: %v", tc.slug, tc.run, err)
			continue
		}
		if gotSlug != tc.slug || gotRun != tc.run {
			t.Errorf("Record() answered %q/%q for %q/%q", gotSlug, gotRun, tc.slug, tc.run)
			continue
		}
		want := "creds/issuance/" + tc.slug + "/" + tc.run
		if got := identitywire.PathRecord(gotSlug, gotRun); got != want {
			t.Errorf("the registration's record lives at %q, want %q", got, want)
		}
	}

	// The job's name, in the one shape the platform gives one.
	for _, tc := range []struct {
		name identitywire.JobName
		want bool
	}{
		{job, true},
		{"projects/acme-sandbox/locations/europe-west1/jobs/fb-1", true},
		{"", false},
		{"fb-1", false},
		{"projects/acme-sandbox/locations/europe-west1/jobs/FB-1", false},
		{"projects/acme-sandbox/locations/europe-west1/jobs/fb-1/executions/e-1", false},
		{"projects/acme_sandbox/locations/europe-west1/jobs/fb-1", false},
		{" projects/acme-sandbox/locations/europe-west1/jobs/fb-1", false},
	} {
		if got := tc.name.Valid(); got != tc.want {
			t.Errorf("JobName(%q).Valid() is %v, want %v", tc.name, got, tc.want)
		}
	}

	// The answer is empty on purpose: a launcher told its record was written
	// has what it needs, and nothing of the record comes back.
	var answer identitywire.JobResponse
	if err := identitywire.DecodeStrict([]byte(`{}`), &answer); err != nil {
		t.Errorf("the registration's answer does not decode from the empty object: %v", err)
	}
}
