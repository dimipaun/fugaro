package installrec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// testBucket is the runs bucket the fake serves these tests from.
const testBucket = "fugaro-runs-acme"

// seed stores one record as the object directly, without a write request:
// what a previous update (or another machine) left there.
func seed(t *testing.T, fake *gcpfake.GCS, text string) {
	t.Helper()
	fake.Put(testBucket, Object, []byte(text))
}

// stored is the object's content, decoded without the package's own reader,
// so a test can check what a write actually left behind.
func stored(t *testing.T, b *blobx.Bucket) (Record, []byte) {
	t.Helper()
	data, _, err := b.Read(context.Background(), Object)
	if err != nil {
		t.Fatalf("reading %s: %v", Object, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("%s does not decode: %v (%s)", Object, err, data)
	}
	return rec, data
}

func TestAdvanceCreatesWhenAbsent(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)

	rec, err := Advance(ctx, b, "0.22.0")
	if err != nil {
		t.Fatalf("advancing an installation that has no record: %v", err)
	}
	want := Record{Format: Format, Release: "0.22.0"}
	if rec != want {
		t.Errorf("record = %+v, want %+v: the first record has no previous release", rec, want)
	}
	got, _ := stored(t, b)
	if got != want {
		t.Errorf("the object holds %+v, want %+v", got, want)
	}
}

func TestAdvanceRaisesAndKeepsPrevious(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)
	seed(t, fake, `{"format":1,"release":"0.21.1","previous_release":""}`)

	rec, err := Advance(ctx, b, "0.22.0")
	if err != nil {
		t.Fatal(err)
	}
	want := Record{Format: Format, Release: "0.22.0", PreviousRelease: "0.21.1"}
	if rec != want {
		t.Errorf("record = %+v, want %+v", rec, want)
	}
	if got, _ := stored(t, b); got != want {
		t.Errorf("the object holds %+v, want %+v", got, want)
	}
}

func TestAdvanceNeverLowers(t *testing.T) {
	ctx := context.Background()
	t.Run("an older release", func(t *testing.T) {
		fake := gcpfake.NewGCS(t)
		b := fake.Bucket(t, testBucket)
		seed(t, fake, `{"format":1,"release":"0.22.0","previous_release":"0.21.1"}`)
		_, before := stored(t, b)

		rec, err := Advance(ctx, b, "0.21.5")
		if err != nil {
			t.Fatal(err)
		}
		if want := (Record{Format: Format, Release: "0.22.0", PreviousRelease: "0.21.1"}); rec != want {
			t.Errorf("record = %+v, want %+v: an older CLI must not lower it", rec, want)
		}
		if _, after := stored(t, b); !bytes.Equal(before, after) {
			t.Errorf("the object changed:\n%s\n%s", before, after)
		}
	})
	t.Run("a version that is not a release", func(t *testing.T) {
		fake := gcpfake.NewGCS(t)
		b := fake.Bucket(t, testBucket)
		seed(t, fake, `{"format":1,"release":"0.22.0","previous_release":"0.21.1"}`)
		_, before := stored(t, b)

		rec, err := Advance(ctx, b, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if rec.Release != "0.22.0" {
			t.Errorf("record = %+v, want the stored release: a dev build names no release", rec)
		}
		if _, after := stored(t, b); !bytes.Equal(before, after) {
			t.Errorf("a version that is not a release reached the object:\n%s\n%s", before, after)
		}
	})
}

func TestAdvanceSameVersionKeepsWindow(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)
	seed(t, fake, `{"format":1,"release":"0.22.0","previous_release":"0.21.1"}`)

	rec, err := Advance(ctx, b, "0.22.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Record{Format: Format, Release: "0.22.0", PreviousRelease: "0.21.1"}); rec != want {
		t.Errorf("record = %+v, want %+v: a re-run keeps the window it found", rec, want)
	}
	if got, _ := stored(t, b); got != rec {
		t.Errorf("the object holds %+v, want %+v", got, rec)
	}
}

// raced is the bucket with one hook: its first replace stores a second
// machine's record first, which is the interleaving a test cannot otherwise
// reach (it takes two updates running at once), and the one the
// generation-matched replace exists for. The replace it is handed is checked
// against the generation the object had before that write: a caller that
// overwrote without matching it would take the other machine's record with
// it, and there is no other way to see that from outside.
type raced struct {
	*blobx.Bucket
	fake     *gcpfake.GCS
	other    []byte
	ran      bool
	mismatch bool
}

func (r *raced) ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error) {
	if !r.ran {
		r.ran = true
		if _, readGen, err := r.Bucket.Read(ctx, key); err == nil && gen != readGen {
			r.mismatch = true
		}
		r.fake.Put(testBucket, key, r.other)
	}
	return r.Bucket.ReplaceIf(ctx, key, data, gen, prev)
}

func TestAdvanceRaceKeepsTheHigher(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)
	seed(t, fake, `{"format":1,"release":"0.21.1","previous_release":""}`)
	w := &raced{Bucket: b, fake: fake, other: []byte(`{"format":1,"release":"0.22.0","previous_release":"0.21.1"}`)}

	rec, err := Advance(ctx, w, "0.21.5")
	if err != nil {
		t.Fatalf("losing the race is not an error: %v", err)
	}
	if !w.ran {
		t.Fatal("the other machine's write never happened, so the test proves nothing")
	}
	if w.mismatch {
		t.Error("the replace was not matched to the generation the read returned, so it would have overwritten the other machine's record")
	}
	if rec.Release != "0.22.0" {
		t.Errorf("record = %+v, want the other machine's release: the higher one stands", rec)
	}
	got, _ := stored(t, b)
	if got.Release != "0.22.0" || got.PreviousRelease != "0.21.1" {
		t.Errorf("the object holds %+v, want the other machine's record", got)
	}
}

func TestAdvanceNotWriterIsASkip(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.DenyWrites(testBucket, "fugaro/")
	b := fake.Bucket(t, testBucket)

	if _, err := Advance(ctx, b, "0.22.0"); !errors.Is(err, ErrNotWriter) {
		t.Fatalf("err = %v, want ErrNotWriter", err)
	}
	if _, _, err := b.Read(ctx, Object); !errors.Is(err, blobx.ErrNotExist) {
		t.Errorf("a refused write left an object behind: %v", err)
	}
}

func TestReadRefusesUnknownFieldAndOversize(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		text string
		want error
	}{
		{"an unknown field", `{"format":1,"release":"0.22.0","previous_release":"","note":"x"}`, nil},
		{"a format this build does not read", `{"format":2,"release":"0.22.0","previous_release":""}`, nil},
		{"a release that is not one", `{"format":1,"release":"main","previous_release":""}`, nil},
		{"data after the object", `{"format":1,"release":"0.22.0","previous_release":""}{}`, nil},
		{"a body past the bound", `{"format":1,"release":"0.22.0","previous_release":"","pad":"` + strings.Repeat("a", 8<<10) + `"}`, blobx.ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := gcpfake.NewGCS(t)
			b := fake.Bucket(t, testBucket)
			seed(t, fake, tc.text)

			_, err := Read(ctx, b)
			if err == nil {
				t.Fatal("Read accepted the document")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadAbsentIsNoWindow(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)

	rec, err := Read(ctx, b)
	if err != nil {
		t.Fatalf("an installation with no record is not an error: %v", err)
	}
	if rec != (Record{}) {
		t.Errorf("record = %+v, want the zero Record: there is no window", rec)
	}
}

// TestReadCanonicalisesTheRelease: updatecheck reads v0.21.1 as the release
// 0.21.1, and the object's schema publishes no v — so the form this package
// hands back, and the form Advance writes into previous_release, is the
// canonical one. A record that carried a v must not put one in a field the
// schema has no room for.
func TestReadCanonicalisesTheRelease(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, testBucket)
	seed(t, fake, `{"format":1,"release":"v0.21.1","previous_release":""}`)

	rec, err := Read(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Record{Format: Format, Release: "0.21.1"}); rec != want {
		t.Errorf("record = %+v, want %+v", rec, want)
	}

	got, err := Advance(ctx, b, "0.22.0")
	if err != nil {
		t.Fatal(err)
	}
	want := Record{Format: Format, Release: "0.22.0", PreviousRelease: "0.21.1"}
	if got != want {
		t.Errorf("record = %+v, want %+v", got, want)
	}
	if onObject, _ := stored(t, b); onObject != want {
		t.Errorf("the object holds %+v, want %+v", onObject, want)
	}
}

// TestRecordMatchesTheSchema holds the record this package writes to the
// published schema of the object: the schema is what a reader outside this
// binary (an operator reading the object, a second machine's tooling) reads
// it by, so a field the record writes and the schema does not declare — or a
// document the record never writes and the schema admits — is drift.
func TestRecordMatchesTheSchema(t *testing.T) {
	data, err := os.ReadFile("../../schemas/installed.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("the schema has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(rec Record) error {
		t.Helper()
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}
	for _, rec := range []Record{
		{Format: Format, Release: "0.22.0"},
		{Format: Format, Release: "0.22.0", PreviousRelease: "0.21.1"},
	} {
		if err := validate(rec); err != nil {
			t.Errorf("the schema refuses the record this package writes (%+v): %v", rec, err)
		}
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"format":1,"release":"0.22.0","previous_release":"","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err == nil {
		t.Error("the schema accepts a field the record does not carry")
	}
}
