package localcfg

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// locationsBase is the file this release writes around the record: the header
// the loader requires, and nothing else. Each case below appends its own
// `locations:` block.
const locationsBase = `version: 3
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
`

// TestLocationsValidate: the record is a reader's statement about the cloud —
// `fugaro doctor` prints a member of it as where the data is (LP12) — so what
// the file says is held to the shape Google spells a location in when the file
// is read, rather than at the reader that would print a non-location as a
// finding. The three members are not one vocabulary: a region for the Realtime
// Database, which has no multi-region, and a region, multi-region or `global`
// for the two things that can be either — so `nam5` is a Firestore or a log
// bucket and is not an RTDB, and the test says so.
func TestLocationsValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
	}{
		{"a full record", "locations:\n  firestore: us-east5\n  rtdb: us-central1\n  secrets: automatic\n  log_bucket: europe-west1\n"},
		{"a multi-region Firestore", "locations:\n  firestore: nam5\n"},
		{"the other multi-regions", "locations:\n  firestore: eur3\n  log_bucket: us\n"},
		{"a global log bucket", "locations:\n  log_bucket: global\n"},
		{"a mixed replication", "locations:\n  secrets: mixed\n"},
		{"regions in place of a word", "locations:\n  secrets: [europe-west1]\n"},
		{"several regions", "locations:\n  secrets: [europe-west1, europe-west2]\n"},
		{"nothing but the block", "locations: {}\n"},
		{"a two-digit region", "locations:\n  rtdb: europe-west12\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(locationsBase + tc.yaml))
			if err != nil {
				t.Fatalf("a valid record was refused: %v", err)
			}
			if c.Locations == nil {
				t.Fatal("the record is absent after a load that read it")
			}
		})
	}

	for _, tc := range []struct{ name, yaml, want string }{
		{"a Firestore location in capitals", "locations:\n  firestore: US-east5\n", "locations.firestore"},
		{"a Firestore location that is no place", "locations:\n  firestore: 9x\n", "locations.firestore"},
		{"a Firestore location with a space", "locations:\n  firestore: us east5\n", "locations.firestore"},
		{"a multi-region Realtime Database", "locations:\n  rtdb: nam5\n", "locations.rtdb"},
		{"a global Realtime Database", "locations:\n  rtdb: global\n", "locations.rtdb"},
		{"a log bucket that is no place", "locations:\n  log_bucket: europe_west1\n", "locations.log_bucket"},
		{"a replication word that is not one", "locations:\n  secrets: auto\n", "locations.secrets"},
		{"a replication word in capitals", "locations:\n  secrets: Automatic\n", "locations.secrets"},
		{"a replication with no region in it", "locations:\n  secrets: []\n", "locations.secrets"},
		{"a replication region that is no region", "locations:\n  secrets: [europe-west1, NOPE]\n", "locations.secrets"},
		{"a replicated-in twice region", "locations:\n  secrets: [europe-west1, europe-west1]\n", "locations.secrets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(locationsBase + tc.yaml))
			if err == nil {
				t.Fatal("the record was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the member: %v", err)
			}
			if !strings.Contains(err.Error(), "local config: ") {
				t.Errorf("the refusal is not the file's own: %v", err)
			}
		})
	}
}

// TestLocationsRoundTrip: the one spelling a policy has is the one it is
// written back as, and a record nobody has read yet is left out of the document
// rather than written as an empty block. It is the HARD RULE at the record's
// own scale: `fugaro update` rewrites this file through the settings path on
// every run, so a block that marshalled an absent record into `locations: {}`
// would rewrite every operator's config — and would tell the next converge's
// reader that a member it holds no answer for is a member somebody decided.
func TestLocationsRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, in, want, prints string }{
		{"a word", "locations:\n  secrets: automatic\n", "    secrets: automatic\n", "automatic"},
		{"mixed", "locations:\n  secrets: mixed\n", "    secrets: mixed\n", "mixed"},
		{"one region", "locations:\n  secrets: [europe-west1]\n", "    secrets:\n        - europe-west1\n", "europe-west1"},
		{"two regions", "locations:\n  secrets: [europe-west1, europe-west2]\n", "    secrets:\n        - europe-west1\n        - europe-west2\n", "europe-west1, europe-west2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(locationsBase + tc.in))
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), "locations:\n"+tc.want) {
				t.Errorf("the record went back as\n%s\nwant\nlocations:\n%s", out, tc.want)
			}
			// What a settings read prints for the row: the regions when the
			// policy names them, else the word — never the two fields of the
			// Go value, which a reader of the table would take for a shape a
			// file has.
			back, err := Parse(out)
			if err != nil {
				t.Fatalf("the document this release wrote does not load: %v", err)
			}
			if got := back.Locations.Secrets.String(); got != tc.prints {
				t.Errorf("the replication prints as %q, want %q", got, tc.prints)
			}
			if back.Locations.Secrets.Kind != c.Locations.Secrets.Kind || !reflect.DeepEqual(back.Locations.Secrets.Regions, c.Locations.Secrets.Regions) {
				t.Errorf("the record came back as %+v, want %+v", back.Locations.Secrets, c.Locations.Secrets)
			}
		})
	}
	// A written `secrets:` with nothing under it is the absent policy, and it
	// is not written back: clearing the member leaves a key behind, and a
	// refusal there would make the clearing a file its author has to repair.
	c, err := Parse([]byte(locationsBase + "locations:\n  firestore: us-east5\n  secrets:\n"))
	if err != nil {
		t.Fatalf("a cleared replication was refused: %v", err)
	}
	if !c.Locations.Secrets.IsZero() {
		t.Errorf("a cleared replication read as %+v", c.Locations.Secrets)
	}
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "secrets") {
		t.Errorf("the cleared replication was written back:\n%s", out)
	}
	// An absent record, and a block with nothing under it, are one statement:
	// not known yet. Neither is written back.
	for _, in := range []string{"", "locations: {}\n"} {
		c, err := Parse([]byte(locationsBase + in))
		if err != nil {
			t.Fatal(err)
		}
		if c.Locations != nil && !c.Locations.Secrets.IsZero() {
			t.Errorf("%q: the replication is not the absent one: %+v", in, c.Locations.Secrets)
		}
		out, err := c.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "locations") {
			t.Errorf("%q: the block was written back:\n%s", in, out)
		}
	}
}

// TestAConfigWithoutLocationsStillLoads: the block is optional and new, and an
// installation that has never converged under this release has none. Such a
// file loads, its record is absent — the reader asks `Locations == nil` and
// every member of the block means "not known yet" only for a block that is
// there — and `fugaro update` writes the file back as it found it.
func TestAConfigWithoutLocationsStillLoads(t *testing.T) {
	c, err := Parse([]byte(locationsBase))
	if err != nil {
		t.Fatalf("a file without the record was refused: %v", err)
	}
	if c.Locations != nil {
		t.Errorf("a file with no record loaded one: %+v", c.Locations)
	}
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "locations") {
		t.Errorf("the absent record was written into the file:\n%s", out)
	}
	// The converge stamps the version, so what a file of this release's own
	// shape goes through on the next update is the same document back.
	again, err := Parse(out)
	if err != nil {
		t.Fatalf("the document this release wrote does not load: %v", err)
	}
	if again.Locations != nil {
		t.Errorf("the record came back from nowhere: %+v", again.Locations)
	}
}

// TestLocationsAreNotPublished: the record is one machine's reading of the
// cloud and is never an input, so it is not published (D3087 as amended after
// review). The reason it may not be published is the shared file's own reader:
// a teammate's Fugaro older than the release that wrote the key decodes the
// published copy strictly (F17), so the key alone refuses that teammate's whole
// file — every run, on a document nobody may edit and nothing in it is for that
// machine to read. The record is dropped by Shared, is not adopted back from a
// published object by MergeShared, and is refused by name where a shared
// config is read (internal/cli's ParseShared, whose own census the key is in).
func TestLocationsAreNotPublished(t *testing.T) {
	const record = "locations:\n  firestore: us-east5\n  rtdb: us-central1\n  secrets: mixed\n  log_bucket: global\n"
	lc, err := Parse([]byte(locationsBase + record))
	if err != nil {
		t.Fatal(err)
	}
	if lc.Locations.Firestore != "us-east5" || lc.Locations.Secrets.Kind != ReplicationMixed {
		t.Fatalf("the fixture's own record was not read: %+v", lc.Locations)
	}
	s := lc.Shared()
	if s.Locations != nil {
		t.Errorf("Shared published the record: %+v", s.Locations)
	}
	if lc.Locations == nil || lc.Locations.Firestore != "us-east5" {
		t.Errorf("Shared cleared the receiver's own record: %+v", lc.Locations)
	}
	out, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "locations") {
		t.Errorf("the published document carries the record:\n%s", out)
	}
	// A published object that carries the key — one written by a binary that
	// publishes it, or a hand-edited file — is not merged from either: the
	// merge output is what goes into the runs bucket, so the key may not reach
	// it by the other door.
	m := MergeShared(lc, lc)
	if m.Locations != nil {
		t.Errorf("MergeShared wrote the record %+v into the file it publishes", m.Locations)
	}
	if m.Name != lc.Name || m.Region != lc.Region {
		t.Errorf("MergeShared dropped a key the record's drop must not touch: %+v", m)
	}
}

// TestAnOlderSharedConfigStillDecodes: the published copy of a machine that
// carries the record decodes on a teammate's Fugaro older than it, which is the
// whole of why the record is not published. The older reader is not a copy of
// this release's reader written out beside it: it is built from Config itself
// with the one field this release added taken back out, so it is exactly the
// reader a teammate has — the same KnownFields(true), the same strictness, one
// field fewer — and it cannot drift from Config without this test saying so.
func TestAnOlderSharedConfigStillDecodes(t *testing.T) {
	lc, err := Parse([]byte(locationsBase + "locations:\n  firestore: us-east5\n  rtdb: us-central1\n  secrets: [europe-west1]\n  log_bucket: global\n"))
	if err != nil {
		t.Fatal(err)
	}
	published, err := lc.Shared().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	older := olderConfigType()

	dec := yaml.NewDecoder(bytes.NewReader(published))
	dec.KnownFields(true)
	if err := dec.Decode(reflect.New(older).Interface()); err != nil {
		t.Errorf("the published copy does not decode with a reader that has no record in it: %v\n%s", err, published)
	}
	// The control: the same reader, on the same document with the key written
	// into it, refuses the whole file — so what the assertion above measures is
	// the key's absence and not the older reader's indifference to it.
	withKey := append(append([]byte{}, published...), []byte("locations:\n  firestore: us-east5\n")...)
	dec = yaml.NewDecoder(bytes.NewReader(withKey))
	dec.KnownFields(true)
	if err := dec.Decode(reflect.New(older).Interface()); err == nil {
		t.Error("a published copy carrying the record decodes with the older reader, so the assertion above proves nothing")
	}
	// A reader of this release reads the published copy too, and finds no
	// record in it: what the published file holds is the file, not the record.
	back, err := Parse(published)
	if err != nil {
		t.Fatalf("this release refuses its own published copy: %v", err)
	}
	if back.Locations != nil {
		t.Errorf("the published copy gave a record back: %+v", back.Locations)
	}
}

// olderConfigType is Config as a Fugaro older than this release has it: the
// same fields with the record taken out. The unexported field the yaml reader
// never reads is left out with it, because reflect cannot build a type that has
// one.
func olderConfigType() reflect.Type {
	ct := reflect.TypeOf(Config{})
	fields := make([]reflect.StructField, 0, ct.NumField())
	for i := range ct.NumField() {
		f := ct.Field(i)
		if f.Name == "Locations" || f.PkgPath != "" {
			continue
		}
		fields = append(fields, f)
	}
	return reflect.StructOf(fields)
}
