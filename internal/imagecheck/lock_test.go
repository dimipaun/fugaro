package imagecheck

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
)

// lockKeyHex is a key of the shape Key returns, and not a real one.
const lockKeyHex = "9f2c1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f"

// lockEntry is one image as §3.1 writes it.
func lockEntry() string {
	return `"` + lockKeyHex + `":{"key":"` + lockKeyHex + `",` +
		`"image":"registry.example/fugaro-shared/img-9f2c1a2b3c4d",` +
		`"digest":"` + baseDigest + `","base_ref":"web","base_digest":"` + baseDigest + `",` +
		`"packages":["left-pad@1.3.0"],"tools":["node@22.11.0"],` +
		`"built_at":"2026-10-29T09:12:04Z","build_id":"1234",` +
		`"repositories":["owner/repo-a"],"jobs":{"owner/repo-a":"fugaro-repo-a-web"},` +
		`"last_used_at":"2026-10-29T09:40:11Z"}`
}

// lockFixtures are the documents the decoder and the published schema are
// both held to, each with whether it is a lock this build reads.
func lockFixtures() []struct {
	name string
	doc  string
	ok   bool
} {
	return []struct {
		name string
		doc  string
		ok   bool
	}{
		{"an installation with no images", `{"version":1,"images":{}}`, true},
		{"one image", `{"version":1,"images":{` + lockEntry() + `}}`, true},
		// §3.2's strictness is about fields, not presence: the decoder
		// refuses a field it does not know and a version it does not read,
		// and says nothing about a field that is missing. An entry the writer
		// would not have written is still read, and the schema admits it too,
		// so the two cannot drift apart on the question.
		{"an image with only a key", `{"version":1,"images":{"` + lockKeyHex + `":{"key":"` + lockKeyHex + `"}}}`, true},
		{"an unknown field beside the version", `{"version":1,"images":{},"cap":20}`, false},
		{"an unknown field in an image", `{"version":1,"images":{` + lockEntry() + `,"cap":20}}`, false},
		{"a version this build does not read", `{"version":2,"images":{}}`, false},
		{"no version at all", `{"images":{}}`, false},
		{"no images object", `{"version":1}`, false},
		{"images that is a list", `{"version":1,"images":[]}`, false},
		{"images that is null", `{"version":1,"images":null}`, false},
		{"a digest that is a number", `{"version":1,"images":{"` + lockKeyHex + `":{"digest":1234}}}`, false},
		{"data after the object", `{"version":1,"images":{}}{"version":1,"images":{}}`, false},
		{"a document that is a list", `[{"version":1,"images":{}}]`, false},
		{"a document that is not JSON", `{"version":1,`, false},
	}
}

// TestLockDecodesStrictly: the lock is read strictly (§3.2), the opposite of
// ParseRecord's deliberate tolerance. Every operator's machine writes this
// one object and every fugaro version in use reads it, so a reader that
// ignored a field it did not know would drop it for everyone the next time
// it wrote the object back; an unknown version stops it and says which.
func TestLockDecodesStrictly(t *testing.T) {
	for _, tc := range lockFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			lock, err := ParseLock([]byte(tc.doc))
			if tc.ok && err != nil {
				t.Fatalf("ParseLock refused a lock: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("ParseLock read a document it must refuse: %+v", lock)
			}
			if tc.ok && lock.Images == nil {
				t.Error("ParseLock left the images map nil, so a caller adding an entry would panic")
			}
		})
	}
	if _, err := ParseLock([]byte(`{"version":2,"images":{}}`)); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Errorf("an unknown version: %v, want the version named", err)
	}
}

// TestLockSchemaAgreesWithTheDecoder: the published schema
// (schemas/image-lock.schema.json) and the decoder accept and refuse the
// same documents. Neither can be widened alone — a document the decoder
// takes and the schema rejects, or the other way round, is one of the two
// lying about what the lock is.
func TestLockSchemaAgreesWithTheDecoder(t *testing.T) {
	data, err := os.ReadFile("../../schemas/image-lock.schema.json")
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
	validates := func(t *testing.T, text string) bool {
		t.Helper()
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
		if err != nil {
			return false
		}
		return sch.Validate(inst) == nil
	}
	for _, tc := range lockFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLock([]byte(tc.doc))
			decoderOK := err == nil
			if schemaOK := validates(t, tc.doc); decoderOK != schemaOK {
				t.Errorf("the decoder says ok=%v and the schema says ok=%v for:\n%s", decoderOK, schemaOK, tc.doc)
			}
		})
	}
}

// fakeLockBucket is a LockWriter a test drives: one object with its
// generation, the competing writer's object to put there first when the
// test wants a race, and the calls it counted.
type fakeLockBucket struct {
	data   []byte
	gen    int64
	exists bool
	key    string
	ct     string
	// race is how many conditional writes another writer gets there first
	// for, and raceData is the object it leaves.
	race     int
	raceData []byte
	creates  int
	replaces int
}

// lose races a conditional write: another writer's object is put there
// first, so the write this fake was asked for cannot win.
func (b *fakeLockBucket) lose() {
	if b.race > 0 {
		b.race--
		b.put(b.raceData)
	}
}

func (b *fakeLockBucket) Read(ctx context.Context, key string) ([]byte, int64, error) {
	b.key = key
	if !b.exists {
		return nil, 0, blobx.ErrNotExist
	}
	return b.data, b.gen, nil
}

func (b *fakeLockBucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	b.creates++
	b.ct = contentType
	b.lose()
	if b.exists {
		return 0, blobx.ErrExists
	}
	b.put(data)
	return b.gen, nil
}

func (b *fakeLockBucket) ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error) {
	b.replaces++
	b.lose()
	if !b.exists || b.gen != gen || !bytes.Equal(b.data, prev) {
		return 0, blobx.ErrConflict
	}
	b.put(data)
	return b.gen, nil
}

func (b *fakeLockBucket) put(data []byte) {
	b.exists, b.data, b.gen = true, data, b.gen+1
}

// addEntry is a plan that records one image and changes nothing else.
func addEntry(key string) func(*Lock) (*Lock, error) {
	return func(l *Lock) (*Lock, error) {
		l.Images[key] = lockedImage(key, resolvedWeb(), supersetBuilt)
		return l, nil
	}
}

// TestLockUpdateWritesByGeneration: §3.4's write. The first write of a
// missing object is a create; every later one is generation-matched against
// the generation the read returned, so two machines refreshing two
// different keys cannot lose each other's entries. A plan that writes
// nothing leaves the object, and its generation, alone.
func TestLockUpdateWritesByGeneration(t *testing.T) {
	ctx := context.Background()
	b := &fakeLockBucket{}
	if err := Update(ctx, b, addEntry(hexKey("9"))); err != nil {
		t.Fatal(err)
	}
	if b.creates != 1 || b.replaces != 0 || b.ct != lockContentType {
		t.Fatalf("the first write: creates=%d replaces=%d content type=%q", b.creates, b.replaces, b.ct)
	}
	if b.key != LockKey || LockKey != "builds/image-lock.json" {
		t.Fatalf("the lock is written to %q, want %q", b.key, LockKey)
	}
	got, err := ParseLock(b.data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Images[hexKey("9")]; !ok || got.Version != LockVersion {
		t.Fatalf("the object the first write left: %+v", got)
	}
	if err := Update(ctx, b, addEntry(hexKey("8"))); err != nil {
		t.Fatal(err)
	}
	if b.creates != 1 || b.replaces != 1 {
		t.Fatalf("the second write: creates=%d replaces=%d", b.creates, b.replaces)
	}
	if got, err = ParseLock(b.data); err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 2 {
		t.Errorf("the object the second write left holds %d images, want 2: %+v", len(got.Images), got.Images)
	}
	if err := Update(ctx, b, func(l *Lock) (*Lock, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if b.creates != 1 || b.replaces != 1 {
		t.Errorf("a plan that writes nothing wrote: creates=%d replaces=%d", b.creates, b.replaces)
	}
}

// TestLockUpdateAdoptsAnotherWritersFirstWrite: two refreshes of two
// different keys both write the one lock object. The one that arrives at an
// empty object loses its create with ErrExists — nothing was lost, since the
// other writer's object is there — re-reads it and adds its own entry to it.
// The object then holds both, and neither refresh built on a stale one.
func TestLockUpdateAdoptsAnotherWritersFirstWrite(t *testing.T) {
	ctx := context.Background()
	theirs := lockOf(lockedImage(hexKey("7"), resolvedWeb(), supersetBuilt))
	data, err := json.Marshal(theirs)
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeLockBucket{race: 1, raceData: data}
	if err := Update(ctx, b, addEntry(hexKey("9"))); err != nil {
		t.Fatal(err)
	}
	if b.creates != 1 || b.replaces != 1 {
		t.Fatalf("creates=%d replaces=%d, want one create that lost and one replace", b.creates, b.replaces)
	}
	got, err := ParseLock(b.data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Images[hexKey("7")]; !ok {
		t.Errorf("the other writer's entry is gone: %+v", got.Images)
	}
	if _, ok := got.Images[hexKey("9")]; !ok {
		t.Errorf("this writer's entry is not there: %+v", got.Images)
	}
}

// TestLockUpdateFailsOnASecondConflict: a writer that loses twice is not
// left guessing. It re-reads and re-plans once, and a second loss exits with
// an error naming the object and the generation it held, for the operator to
// rerun.
func TestLockUpdateFailsOnASecondConflict(t *testing.T) {
	ctx := context.Background()
	theirs := lockOf(lockedImage(hexKey("7"), resolvedWeb(), supersetBuilt))
	data, err := json.Marshal(theirs)
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeLockBucket{race: 2, raceData: data}
	err = Update(ctx, b, addEntry(hexKey("9")))
	if err == nil {
		t.Fatal("a writer that lost twice reported success")
	}
	for _, want := range []string{LockKey, "generation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
	if !errors.Is(err, blobx.ErrConflict) {
		t.Errorf("the error %q does not wrap the conflict the bucket answered", err)
	}
	if b.creates+b.replaces != 2 {
		t.Errorf("the write was attempted %d times, want 2 (a re-plan once, then give up)", b.creates+b.replaces)
	}
}
