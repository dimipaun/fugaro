package runmsg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const (
	testSlug = "acme-app"
	testRun  = "20261119-041950-d315"
)

var t0 = time.Date(2026, 11, 19, 4, 20, 0, 0, time.UTC)

// testBucket is a bucket named as the installation names the runs bucket,
// with no IAM in front of it: the store's own answers are what these tests
// read.
func testBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	return gcpfake.NewGCS(t).Bucket(t, "runs")
}

// seed writes a message object directly, the way a launcher holding
// objectUser on tokens/ would: the store is not asked, so a test can build
// the shapes a hand-written object has.
func seed(t *testing.T, b *blobx.Bucket, seq int, m Message) {
	t.Helper()
	m.Version, m.Seq = Format, seq
	if m.SentAt.IsZero() {
		m.SentAt = t0
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Create(context.Background(), Path(testSlug, testRun, seq), raw, "application/json"); err != nil {
		t.Fatal(err)
	}
}

// seedRaw writes bytes a test builds itself, for the shapes a Message cannot
// carry.
func seedRaw(t *testing.T, b *blobx.Bucket, seq int, raw []byte) {
	t.Helper()
	if _, err := b.Create(context.Background(), Path(testSlug, testRun, seq), raw, "application/json"); err != nil {
		t.Fatal(err)
	}
}

// listed is the message numbers the store's own listing sees, in order.
func listed(t *testing.T, b *blobx.Bucket) []int {
	t.Helper()
	nums, err := numbers(context.Background(), b, testSlug, testRun)
	if err != nil {
		t.Fatal(err)
	}
	return nums
}

// TestPathUnderTokens pins where a message object lives, which is the whole of
// what makes it unforgeable from inside a run (D2941, F2): the job account
// holds a read-only grant on tokens/ and objectUser on runs/, so a message
// under runs/ could be planted by the agent the message is meant to steer.
// The prefix is token.ObjectPrefix, never a second literal that could drift
// from the grants the Terraform modules put on exactly that name.
//
// Mutation (move Path under runs/, or spell the prefix out again): the
// comparison against token.ObjectPrefix and the "not under runs/" assertion
// both fail.
func TestPathUnderTokens(t *testing.T) {
	got := Path(testSlug, testRun, 7)
	want := token.ObjectPrefix + "/" + testSlug + "/" + testRun + "/messages/0007.msg.json"
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, token.ObjectPrefix+"/") {
		t.Errorf("Path %q is not under %s/, the one prefix a job account only reads", got, token.ObjectPrefix)
	}
	if strings.HasPrefix(got, "runs/") {
		t.Errorf("Path %q is under runs/, where a run's own agent can write it", got)
	}
	if !strings.HasSuffix(got, Suffix) {
		t.Errorf("Path %q does not end in %s, the suffix the lifecycle rule reaps by", got, Suffix)
	}
	if p := Prefix(testSlug, testRun); p != token.ObjectPrefix+"/"+testSlug+"/"+testRun+"/messages/" {
		t.Errorf("Prefix = %q", p)
	}
	// The number is zero-padded to four digits, so a listing sorts the way the
	// runner walks it.
	if p := Path(testSlug, testRun, 1); !strings.HasSuffix(p, "/0001"+Suffix) {
		t.Errorf("Path(1) = %q, want it to end in /0001%s", p, Suffix)
	}
}

// TestMessageSchemaRoundTrip: a message the store writes is a document
// schemas/run-message.schema.json accepts and the store's own strict decoder
// reads back to the same value. The schema is what a person checks a
// hand-written object against and the decoder is the authority, so the two
// are held together here, the way internal/access holds its node's schema to
// its reader.
//
// Mutation (drop a field from the schema, rename a Go tag, or loosen the
// decoder): the marshalled message is refused, or the round trip comes back
// different.
func TestMessageSchemaRoundTrip(t *testing.T) {
	sch := messageSchema(t)
	m := Message{
		Version: Format,
		Seq:     3,
		SentAt:  t0,
		SentBy:  "dimi@example.invalid",
		Flags:   []string{FlagNow},
		Text:    "use the existing helper in internal/foo, not a new one",
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the schema refuses a message the store writes: %v\n%s", err, raw)
	}
	back, err := decodeStrict(raw)
	if err != nil {
		t.Fatalf("the store refuses a message it wrote: %v\n%s", err, raw)
	}
	if !reflect.DeepEqual(*back, m) {
		t.Fatalf("round trip = %+v, want %+v", *back, m)
	}
	// The other direction, for the fields a hand-written object could get
	// wrong: a document the schema refuses is one the decoder refuses too.
	for _, tc := range []struct{ name, doc string }{
		{"a version the format does not have", `{"version":2,"seq":1,"sent_at":"2026-11-19T04:20:00Z","text":"hi"}`},
		{"a number past the cap", `{"version":1,"seq":21,"sent_at":"2026-11-19T04:20:00Z","text":"hi"}`},
		{"no text", `{"version":1,"seq":1,"sent_at":"2026-11-19T04:20:00Z"}`},
		{"a flag the format does not know", `{"version":1,"seq":1,"sent_at":"2026-11-19T04:20:00Z","flags":["someday"],"text":"hi"}`},
		{"a field the format does not hold", `{"version":1,"seq":1,"sent_at":"2026-11-19T04:20:00Z","text":"hi","urgent":true}`},
	} {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.doc))
		if err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(inst); err == nil {
			t.Errorf("the schema accepts %s: %s", tc.name, tc.doc)
		}
		if _, err := decodeStrict([]byte(tc.doc)); err == nil {
			t.Errorf("the decoder accepts %s: %s", tc.name, tc.doc)
		}
	}
}

// TestPutTakesNextNumber: Put numbers a message itself, from the listing, and
// the object lands where Path says. A message is written once and never
// rewritten: the number is the identity a later stage and the record refer to.
func TestPutTakesNextNumber(t *testing.T) {
	ctx := context.Background()
	b := testBucket(t)
	for i, text := range []string{"first", "second", "third"} {
		seq, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: text})
		if err != nil {
			t.Fatalf("Put(%q) = %v", text, err)
		}
		if seq != i+1 {
			t.Fatalf("Put(%q) took number %d, want %d", text, seq, i+1)
		}
	}
	if got := listed(t, b); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("the listing holds %v, want [1 2 3]", got)
	}
	m, err := Next(ctx, b, testSlug, testRun, 2)
	if err != nil || m == nil {
		t.Fatalf("Next(2) = %v, %v", m, err)
	}
	if m.Text != "second" || m.Seq != 2 {
		t.Fatalf("Next(2) = %+v", *m)
	}
	// Put stamps the send time, so the record's sent_at is the moment the
	// object was written and not a zero a reader would have to explain.
	if m.SentAt.IsZero() {
		t.Error("Put wrote a message with no sent_at")
	}
}

// TestPutCollisionRetries: two senders list the same highest number and both
// write it; the create is create-if-absent, so one loses and takes the next
// number instead of overwriting the other's message (F10).
//
// The seam is the window between the listing and the write, which is where
// the race happens; production leaves it nil.
func TestPutCollisionRetries(t *testing.T) {
	ctx := context.Background()
	b := testBucket(t)
	took := Path(testSlug, testRun, 1)
	beforeCreate = func(key string) {
		if key != took {
			return
		}
		// The other sender wins the number while this one is between its
		// listing and its write.
		beforeCreate = nil
		seed(t, b, 1, Message{Text: "the other sender's"})
	}
	t.Cleanup(func() { beforeCreate = nil })

	seq, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: "mine"})
	if err != nil {
		t.Fatalf("Put = %v", err)
	}
	if seq != 2 {
		t.Fatalf("Put took number %d, want 2 after losing the race for 1", seq)
	}
	other, err := Next(ctx, b, testSlug, testRun, 1)
	if err != nil || other == nil || other.Text != "the other sender's" {
		t.Fatalf("message 1 = %+v, %v, want the other sender's untouched", other, err)
	}
	mine, err := Next(ctx, b, testSlug, testRun, 2)
	if err != nil || mine == nil || mine.Text != "mine" {
		t.Fatalf("message 2 = %+v, %v", mine, err)
	}
}

// TestPutRefusesGap: a number missing below the highest one is a listing this
// sender must not paper over, because writing past the gap would leave the
// runner's numbering with a hole it cannot tell from a message nobody wrote
// yet (F10). It names the missing number and writes nothing.
func TestPutRefusesGap(t *testing.T) {
	ctx := context.Background()
	b := testBucket(t)
	seed(t, b, 1, Message{Text: "one"})
	seed(t, b, 3, Message{Text: "three"})

	_, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: "four"})
	if !errors.Is(err, ErrGap) {
		t.Fatalf("Put over a gap = %v, want ErrGap", err)
	}
	if !strings.Contains(err.Error(), "0002") {
		t.Errorf("the refusal does not name the missing number: %v", err)
	}
	if got := listed(t, b); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("the refusal still wrote: the listing holds %v", got)
	}
}

// TestNextMissingIsNone: a number with no message is "none yet", never an
// error, and a job account's 403 for a missing object is the same answer
// (F10): the grant on tokens/ carries no storage.objects.list, so GCS refuses
// the read of an object that is not there. A number past the cap is never
// read at all, whatever is stored at it (F12).
func TestNextMissingIsNone(t *testing.T) {
	ctx := context.Background()
	b := testBucket(t)
	if m, err := Next(ctx, b, testSlug, testRun, 1); err != nil || m != nil {
		t.Fatalf("Next over a 404 = %v, %v, want none", m, err)
	}
	fake := gcpfake.NewGCS(t)
	fake.AnswerMissingAs403("runs", token.ObjectPrefix+"/")
	if m, err := Next(ctx, fake.Bucket(t, "runs"), testSlug, testRun, 1); err != nil || m != nil {
		t.Fatalf("Next over a 403 = %v, %v, want none", m, err)
	}
	// The runner reads 1..MaxMessages and nothing above: an object numbered
	// 21 is there and is still never read.
	seed(t, b, MaxMessages+1, Message{Text: "over the cap"})
	if m, err := Next(ctx, b, testSlug, testRun, MaxMessages+1); err != nil || m != nil {
		t.Fatalf("Next(%d) = %v, %v, want none without reading the object", MaxMessages+1, m, err)
	}
}

// TestNextRefusesAnUnreadableMessage: an object under the prefix that is not a
// message this build reads is refused rather than delivered (rule 6): the
// prefix is writable by every launcher, by hand included, so the runner must
// never splice bytes it does not understand into a prompt. The refusal is
// ErrRefused, which is what the runner records as `refused`.
func TestNextRefusesAnUnreadableMessage(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		seq  int
		raw  []byte
	}{
		{"not JSON at all", 1, []byte("hello")},
		{"a version this build does not read", 2, []byte(`{"version":2,"seq":2,"sent_at":"2026-11-19T04:20:00Z","text":"hi"}`)},
		{"a number that is not the object's", 3, []byte(`{"version":1,"seq":4,"sent_at":"2026-11-19T04:20:00Z","text":"hi"}`)},
		{"text over the cap", 4, []byte(`{"version":1,"seq":4,"sent_at":"2026-11-19T04:20:00Z","text":"` + strings.Repeat("x", MaxText+1) + `"}`)},
		{"a field this build does not know", 5, []byte(`{"version":1,"seq":5,"sent_at":"2026-11-19T04:20:00Z","text":"hi","urgent":true}`)},
		{"two values in one object", 6, []byte(`{"version":1,"seq":6,"sent_at":"2026-11-19T04:20:00Z","text":"hi"}{"version":1}`)},
	} {
		b := testBucket(t)
		seedRaw(t, b, tc.seq, tc.raw)
		m, err := Next(ctx, b, testSlug, testRun, tc.seq)
		if !errors.Is(err, ErrRefused) {
			t.Errorf("%s: Next = %v, %v, want ErrRefused", tc.name, m, err)
		}
	}
}

// TestCapsRefused: the size cap and the count cap are held at the write, so
// no sender can put an object there the runner would have to read and then
// throw away (F12), and the read side refuses the same shapes a hand-written
// object can carry. The boundary is pinned on both sides: a text exactly at
// the cap is written and read.
func TestCapsRefused(t *testing.T) {
	ctx := context.Background()
	b := testBucket(t)
	atCap := strings.Repeat("x", MaxText)
	if _, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: atCap}); err != nil {
		t.Fatalf("Put at the text cap = %v, want it written", err)
	}
	if m, err := Next(ctx, b, testSlug, testRun, 1); err != nil || m == nil || len(m.Text) != MaxText {
		t.Fatalf("Next at the text cap = %+v, %v", m, err)
	}
	for _, tc := range []struct {
		name string
		msg  Message
		want error
	}{
		{"text over the cap", Message{Version: Format, Text: atCap + "x"}, ErrTooLong},
		{"no text", Message{Version: Format}, ErrEmpty},
		{"a sender over the cap", Message{Version: Format, Text: "hi", SentBy: strings.Repeat("s", MaxSender+1)}, ErrTooLong},
		{"a version this build does not write", Message{Version: Format + 1, Text: "hi"}, ErrShape},
		{"a flag this build does not know", Message{Version: Format, Text: "hi", Flags: []string{"someday"}}, ErrShape},
		{"a flag listed twice", Message{Version: Format, Text: "hi", Flags: []string{FlagNow, FlagNow}}, ErrShape},
	} {
		if _, err := Put(ctx, b, testSlug, testRun, tc.msg); !errors.Is(err, tc.want) {
			t.Errorf("%s: Put = %v, want %v", tc.name, err, tc.want)
		}
	}
	// The count cap: the 21st message is refused, and the run keeps the 20 it
	// has.
	for i := 2; i <= MaxMessages; i++ {
		if _, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: "filler"}); err != nil {
			t.Fatalf("Put #%d = %v", i, err)
		}
	}
	if _, err := Put(ctx, b, testSlug, testRun, Message{Version: Format, Text: "the 21st"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("the 21st message = %v, want ErrTooMany", err)
	}
	if got := len(listed(t, b)); got != MaxMessages {
		t.Fatalf("the run holds %d messages after the refusal, want %d", got, MaxMessages)
	}
}

// messageSchema compiles schemas/run-message.schema.json under its own $id,
// the way a person validating a hand-written object would.
func messageSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "run-message.schema.json"))
	if err != nil {
		t.Fatalf("reading the message's schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the message's schema is not JSON: %v", err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("the message's schema carries no $id, so nothing can compile it")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("compiling the message's schema: %v", err)
	}
	return sch
}
