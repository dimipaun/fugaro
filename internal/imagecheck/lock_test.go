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

// otherLockKeyHex is a second key of that shape: two of them are what the
// one-job rule is about.
const otherLockKeyHex = "0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"

// entryUnder is one image as §3.1 writes it, keyed by key.
func entryUnder(key string) string {
	return `"` + key + `":{"key":"` + key + `",` +
		`"image":"registry.example/fugaro-shared/img-` + key[:12] + `",` +
		`"digest":"` + baseDigest + `","base_ref":"web","base_digest":"` + baseDigest + `",` +
		`"packages":["\"left-pad\" \"1.3.0\" \"\""],"tools":["node@22.11.0"],` +
		`"built_at":"2026-10-29T09:12:04Z","build_id":"1234",` +
		`"repositories":["owner/repo-a"],"jobs":{"owner/repo-a":"fugaro-repo-a-web"},` +
		`"last_used_at":"2026-10-29T09:40:11Z"}`
}

// lockEntry is one image, under the key these tests' locks store it.
func lockEntry() string { return entryUnder(lockKeyHex) }

// twoImagesOneJob is one repository's job placed on two images: each entry is
// one the writer could have written, and the pair of them is not.
func twoImagesOneJob() string {
	return `{"version":1,"images":{` + entryUnder(lockKeyHex) + `,` + entryUnder(otherLockKeyHex) + `}}`
}

// nullIsAbsent is why the four null fixtures below read differently: a JSON
// null into a slice or a map is a no-op for encoding/json, so the decoder
// reads the field as absent — which §3.2 does not refuse, the same reading
// that lets an image carry only a key — while the schema's array and object
// types refuse null.
const nullIsAbsent = "a null is an absent field to encoding/json and a type error to the schema"

// lockFixture is one document the decoder and the published schema are both
// held to. ok is ParseLock's answer and schema the schema's; why is set on
// the documents the two read differently and says which side is stricter,
// so each difference is pinned here rather than discovered later.
type lockFixture struct {
	name   string
	doc    string
	ok     bool
	schema bool
	why    string
}

// lockFixtures are the documents both are held to. A fixture the two agree
// on carries the same answer twice.
func lockFixtures() []lockFixture {
	return []lockFixture{
		{name: "an installation with no images", doc: `{"version":1,"images":{}}`, ok: true, schema: true},
		{name: "one image", doc: `{"version":1,"images":{` + lockEntry() + `}}`, ok: true, schema: true},
		// §3.2's strictness is about fields, not presence: the decoder
		// refuses a field it does not know and a version it does not read,
		// and says nothing about a field that is missing. An entry the writer
		// would not have written is still read, and the schema admits it too,
		// so the two cannot drift apart on the question.
		{name: "an image with only a key", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"key":"` + lockKeyHex + `"}}}`, ok: true, schema: true},
		{name: "an unknown field beside the version", doc: `{"version":1,"images":{},"cap":20}`, ok: false, schema: false},
		{name: "an unknown field in an image", doc: `{"version":1,"images":{` + lockEntry() + `,"cap":20}}`, ok: false, schema: false},
		{name: "a version this build does not read", doc: `{"version":2,"images":{}}`, ok: false, schema: false},
		{name: "no version at all", doc: `{"images":{}}`, ok: false, schema: false},
		{name: "no images object", doc: `{"version":1}`, ok: false, schema: false},
		{name: "images that is a list", doc: `{"version":1,"images":[]}`, ok: false, schema: false},
		{name: "images that is null", doc: `{"version":1,"images":null}`, ok: false, schema: false},
		{name: "a digest that is a number", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"digest":1234}}}`, ok: false, schema: false},
		{name: "data after the object", doc: `{"version":1,"images":{}}{"version":1,"images":{}}`, ok: false, schema: false},
		{name: "a document that is a list", doc: `[{"version":1,"images":{}}]`, ok: false, schema: false},
		{name: "a document that is not JSON", doc: `{"version":1,`, ok: false, schema: false},
		// The version, written so that it is the number 1 and not the token
		// `1`. JSON Schema reads numbers by value, so it has no way to say
		// "exactly this token" and admits both; encoding/json refuses a
		// fraction and an exponent for an int field. The decoder is the
		// stricter of the two here, and every version a writer could mean is
		// refused by both.
		{name: "a version written as a fraction", doc: `{"version":1.0,"images":{}}`, ok: false, schema: true,
			why: "1.0 is the number 1 to a schema that reads numbers by value, and a type error to encoding/json"},
		{name: "a version written as an exponent", doc: `{"version":1e0,"images":{}}`, ok: false, schema: true,
			why: "1e0 is the number 1 to a schema that reads numbers by value, and a type error to encoding/json"},
		// The same reading of a null, on each field that holds a list or an
		// object. These are the schema's stricter half.
		{name: "a null packages list", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"packages":null}}}`, ok: true, schema: false, why: nullIsAbsent},
		{name: "a null tools list", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"tools":null}}}`, ok: true, schema: false, why: nullIsAbsent},
		{name: "a null repositories list", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"repositories":null}}}`, ok: true, schema: false, why: nullIsAbsent},
		{name: "a null jobs object", doc: `{"version":1,"images":{"` + lockKeyHex + `":{"jobs":null}}}`, ok: true, schema: false, why: nullIsAbsent},
		// A rule about two entries at once, and the one difference the
		// schema cannot state: each entry of this lock is one the writer
		// could have written, and the pair of them is not (see
		// TestLockRefusesTwoKeysForOneRepositoryWorkflow).
		{name: "one job on two images", doc: twoImagesOneJob(), ok: false, schema: true,
			why: "one job on two entries is a rule about two entries at once, and a schema that validates one entry has no way to state it"},
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
// same documents, and the three differences that exist are the fixtures'
// stated ones rather than a hole. Neither can be widened alone — a document
// the decoder takes and the schema rejects, or the other way round, is one
// of the two lying about what the lock is.
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
			if got := validates(t, tc.doc); got != tc.schema {
				t.Errorf("the schema says ok=%v, want %v for:\n%s", got, tc.schema, tc.doc)
			}
			if tc.ok != tc.schema && tc.why == "" {
				t.Errorf("the decoder says ok=%v and the schema says ok=%v for:\n%s\nand the fixture gives no reason", tc.ok, tc.schema, tc.doc)
			}
		})
	}
}

// TestLockRefusesTwoKeysForOneRepositoryWorkflow: a job is one repository and
// one workflow, and it runs one image. Were a lock to place one job on two
// entries, a launch asking which image a job is on (§9's question) would
// answer with whichever entry it met first — and so would the job's image
// depend on map order — so the decoder refuses such a lock and names the job
// and both keys it found it on.
func TestLockRefusesTwoKeysForOneRepositoryWorkflow(t *testing.T) {
	_, err := ParseLock([]byte(twoImagesOneJob()))
	if err == nil {
		t.Fatal("ParseLock read a lock placing one job on two images")
	}
	for _, want := range []string{"fugaro-repo-a-web", "owner/repo-a", lockKeyHex, otherLockKeyHex} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
	// One repository with two workflows is two jobs on two images, which is
	// the ordinary case: the rule is about a job, not about a repository.
	two := `{"version":1,"images":{` + entryUnder(lockKeyHex) + `,` +
		strings.Replace(entryUnder(otherLockKeyHex), "fugaro-repo-a-web", "fugaro-repo-a-api", 1) + `}}`
	if _, err := ParseLock([]byte(two)); err != nil {
		t.Errorf("ParseLock refused one repository's two workflows on two images: %v", err)
	}
	// Two repositories whose jobs happen to carry the SAME name are two jobs
	// too, and they may be on two images: a repository's job names are its own,
	// so reading the name alone would refuse this lock. One job RENAMED between
	// two entries is what neither reading catches — §3.1 names that as the
	// refresh's job, C8b1b — because a name is all the lock states of a job.
	twoRepos := `{"version":1,"images":{` + entryUnder(lockKeyHex) + `,` +
		strings.ReplaceAll(entryUnder(otherLockKeyHex), "owner/repo-a", "owner/repo-b") + `}}`
	if _, err := ParseLock([]byte(twoRepos)); err != nil {
		t.Errorf("ParseLock refused two repositories' same-named jobs on two images: %v", err)
	}
}

// TestLockReadForbiddenIsAnError: the lock's read is strict, so a refusal is
// an error naming the remedy and never an absent lock. The two answers send a
// caller opposite ways — one is a permissions fault to fix, the other is an
// installation that may simply not use the shared images yet — and a caller
// that read a 403 as absence would permit what it cannot see.
func TestLockReadForbiddenIsAnError(t *testing.T) {
	ctx := context.Background()
	refused := &fakeLockBucket{readErr: blobx.ErrForbidden}
	lock, err := ReadLock(ctx, refused)
	if lock != nil || err == nil {
		t.Fatalf("ReadLock on a refused read: lock=%+v err=%v", lock, err)
	}
	if !errors.Is(err, blobx.ErrForbidden) {
		t.Errorf("the error %q does not wrap the bucket's refusal", err)
	}
	for _, want := range []string{LockKey, "403", "read access"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}

	// An installation with no lock is not an error, and the lock comes back
	// when it is there.
	none := &fakeLockBucket{}
	if lock, err = ReadLock(ctx, none); lock != nil || err != nil {
		t.Errorf("ReadLock with no object: lock=%+v err=%v", lock, err)
	}
	if none.key != LockKey {
		t.Errorf("ReadLock read %q, want %q", none.key, LockKey)
	}
	there := &fakeLockBucket{exists: true, data: []byte(`{"version":1,"images":{` + lockEntry() + `}}`)}
	if lock, err = ReadLock(ctx, there); err != nil || lock == nil || len(lock.Images) != 1 {
		t.Errorf("ReadLock of a lock that is there: lock=%+v err=%v", lock, err)
	}
	// A lock this build cannot read is that error, not an absent one.
	older := &fakeLockBucket{exists: true, data: []byte(`{"version":2,"images":{}}`)}
	if _, err = ReadLock(ctx, older); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Errorf("ReadLock of a lock from a newer fugaro: %v", err)
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
	// readErr and createErr, when set, are what the read and the conditional
	// write answer instead of doing anything.
	readErr   error
	createErr error
	creates   int
	replaces  int
}

// lose races a conditional write: another writer's object is put there
// first, so the write this fake was asked for cannot win.
func (b *fakeLockBucket) lose() {
	if b.race > 0 {
		b.race--
		b.put(b.raceData)
	}
}

func (b *fakeLockBucket) ReadStrict(ctx context.Context, key string) ([]byte, int64, error) {
	b.key = key
	if b.readErr != nil {
		return nil, 0, b.readErr
	}
	if !b.exists {
		return nil, 0, blobx.ErrNotExist
	}
	return b.data, b.gen, nil
}

func (b *fakeLockBucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	b.creates++
	b.ct = contentType
	if b.createErr != nil {
		return 0, b.createErr
	}
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

// TestLockUpdateFailsOnASecondConflict: a writer that loses its create to
// another writer and then conflicts twice is not left guessing. It re-reads
// and re-plans once, and the second conflict exits with an error naming the
// object and the generation it held, for the operator to rerun.
func TestLockUpdateFailsOnASecondConflict(t *testing.T) {
	ctx := context.Background()
	theirs := lockOf(lockedImage(hexKey("7"), resolvedWeb(), supersetBuilt))
	data, err := json.Marshal(theirs)
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeLockBucket{race: 3, raceData: data}
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
	// Three writes: the create that lost to the other writer's object — which
	// §3.4 does not charge against the budget, since nothing was lost — then
	// the re-plan, then the conflict that gives up.
	if b.creates != 1 || b.replaces != 2 {
		t.Errorf("creates=%d replaces=%d, want one lost create and two conflicts", b.creates, b.replaces)
	}
}

// TestLockUpdateNormalisesWhatItWrites: a plan may hand back a lock it built
// itself, and a fresh one holds a zero version and a nil images map — an
// object ParseLock and the schema both refuse. Those two are normalised
// rather than stored, and anything else this package could not read back is
// refused before it is written, so a write never leaves the one source of
// every repository's image digest unreadable while reporting success.
func TestLockUpdateNormalisesWhatItWrites(t *testing.T) {
	ctx := context.Background()
	b := &fakeLockBucket{}
	fresh := func(*Lock) (*Lock, error) {
		return &Lock{Images: map[string]LockedImage{hexKey("9"): lockedImage(hexKey("9"), resolvedWeb(), supersetBuilt)}}, nil
	}
	if err := Update(ctx, b, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := ParseLock(b.data)
	if err != nil {
		t.Fatalf("the object written is not one ParseLock reads: %v", err)
	}
	if got.Version != LockVersion || len(got.Images) != 1 {
		t.Errorf("the object written: %+v", got)
	}

	empty := &fakeLockBucket{}
	if err := Update(ctx, empty, func(*Lock) (*Lock, error) { return &Lock{}, nil }); err != nil {
		t.Fatal(err)
	}
	if got, err = ParseLock(empty.data); err != nil {
		t.Fatalf("the object written is not one ParseLock reads: %v", err)
	}
	if got.Images == nil {
		t.Error("the object written holds a null images map")
	}

	refused := &fakeLockBucket{}
	err = Update(ctx, refused, func(l *Lock) (*Lock, error) { l.Version = 2; return l, nil })
	if err == nil {
		t.Fatal("a plan writing a lock version this build does not read reported success")
	}
	if !strings.Contains(err.Error(), "version 2") {
		t.Errorf("the error %q does not name the version", err)
	}
	if refused.creates != 0 || refused.replaces != 0 {
		t.Errorf("the refused lock was written: creates=%d replaces=%d", refused.creates, refused.replaces)
	}
}

// TestLockUpdateReportsAnAccessErrorAsItself: the read is strict, so a
// refusal is never mistaken for an empty lock. A read this machine may not
// make comes back as the access error it is and no create follows it —
// through the tolerant read a 403 would look like an absent object, and the
// create after it would lose with ErrExists, which is a permissions fault
// reported as another writer and a lock that is never written.
func TestLockUpdateReportsAnAccessErrorAsItself(t *testing.T) {
	ctx := context.Background()
	b := &fakeLockBucket{readErr: blobx.ErrForbidden}
	err := Update(ctx, b, addEntry(hexKey("9")))
	if !errors.Is(err, blobx.ErrForbidden) {
		t.Errorf("the error %q is not the read's refusal", err)
	}
	if b.creates != 0 || b.replaces != 0 {
		t.Errorf("a write followed a refused read: creates=%d replaces=%d", b.creates, b.replaces)
	}
}

// TestLockUpdateDoesNotSpinOnALostCreate: a lost create does not spend the
// one re-plan a conflict costs, so a bucket that answered ErrExists for an
// object that is not there would otherwise loop forever. The write gives up
// after lockWriteAttempts and says what it was doing.
func TestLockUpdateDoesNotSpinOnALostCreate(t *testing.T) {
	ctx := context.Background()
	b := &fakeLockBucket{createErr: blobx.ErrExists}
	err := Update(ctx, b, addEntry(hexKey("9")))
	if err == nil {
		t.Fatal("a create that kept losing reported success")
	}
	if !strings.Contains(err.Error(), LockKey) {
		t.Errorf("the error %q does not name the object", err)
	}
	if b.creates != lockWriteAttempts {
		t.Errorf("the create was attempted %d times, want %d", b.creates, lockWriteAttempts)
	}
}
