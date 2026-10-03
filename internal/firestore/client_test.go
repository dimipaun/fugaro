package firestore_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const proj = "aurora-fp-123"

func newClient(t *testing.T, f *gcpfake.Firestore, opts ...firestore.Option) *firestore.Client {
	t.Helper()
	c, err := firestore.New(f.URL, proj, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tok-SECRET"}), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	ts := time.Date(2026, 10, 3, 1, 2, 3, 456000, time.UTC)
	in := map[string]any{
		"s": "x", "b": true, "n": nil, "i": int64(-9007199254740993), "f": 1.5, "t": ts,
		"m":       map[string]any{"a.b": map[string]any{"micros": int64(7)}, "": nil}[""],
		"a":       []any{"x", int64(2), map[string]any{"k": false}},
		"byModel": map[string]any{"claude-opus-4%2E5": map[string]any{"micros": int64(12), "in": int64(3)}},
	}
	enc, err := firestore.EncodeFields(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := enc["i"].(map[string]any)["integerValue"]; got != "-9007199254740993" {
		t.Fatalf("integer on the wire = %#v, want a decimal string", got)
	}
	out, err := firestore.DecodeFields(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip:\n got %#v\nwant %#v", out, in)
	}
	for name, bad := range map[string]any{
		"unsupported": struct{}{}, "nan": func() any { var z float64; return z / z }(),
	} {
		if _, err := firestore.Encode(bad); err == nil && name != "nan" {
			t.Errorf("%s: encoded", name)
		}
	}
	for name, v := range map[string]any{
		"two kinds":  map[string]any{"stringValue": "a", "integerValue": "1"},
		"int number": map[string]any{"integerValue": float64(1)},
		"unknown":    map[string]any{"bytesValue": "AA=="},
		"not a map":  "x",
	} {
		if _, err := firestore.Decode(v); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}

func TestPatchMaskReplacesMaps(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	f.Set("spendDaily", "d1", map[string]any{
		"date": "2026-10-01", "keep": int64(5), "capDailyMicros": int64(9),
		"byModel": map[string]any{"old": map[string]any{"micros": int64(1)}, "gone": map[string]any{"micros": int64(2)}},
	})
	c := newClient(t, f)
	doc, err := c.Patch(context.Background(), "spendDaily", "d1", map[string]any{
		"byModel":   map[string]any{"new": map[string]any{"micros": int64(3)}},
		"by-person": map[string]any{"a@b.c": map[string]any{"runs": int64(1)}}, // not a simple name: backticked
		"spent":     int64(10),
	}, firestore.Clear("capDailyMicros"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"date": "2026-10-01", "keep": int64(5), "spent": int64(10),
		"byModel":   map[string]any{"new": map[string]any{"micros": int64(3)}},
		"by-person": map[string]any{"a@b.c": map[string]any{"runs": int64(1)}},
	}
	got, _ := f.Value("spendDaily", "d1")
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(doc.Fields, want) {
		t.Fatalf("stored %#v\nreturned %#v\nwant %#v", got, doc.Fields, want)
	}
	if doc.Collection != "spendDaily" || doc.ID != "d1" || doc.UpdateTime == "" {
		t.Fatalf("doc = %+v", doc)
	}
	// Creates when absent.
	if _, err := c.Patch(context.Background(), "spendDaily", "d2", map[string]any{"date": "2026-10-02"}); err != nil {
		t.Fatal(err)
	}
	if ids := f.IDs("spendDaily"); !reflect.DeepEqual(ids, []string{"d1", "d2"}) {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := c.Patch(context.Background(), "spendDaily", "d3", nil); err == nil {
		t.Fatal("an empty patch must be refused")
	}
}

func TestPatchPreconditions(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	ctx := context.Background()
	if _, err := c.Patch(ctx, "c", "x", map[string]any{"a": int64(1)}, firestore.MustExist()); !errors.Is(err, firestore.ErrNotFound) {
		t.Fatalf("MustExist on absent: %v", err)
	}
	d, err := c.Patch(ctx, "c", "x", map[string]any{"a": int64(1)}, firestore.MustNotExist())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Patch(ctx, "c", "x", map[string]any{"a": int64(2)}, firestore.MustNotExist()); !errors.Is(err, firestore.ErrPrecondition) {
		t.Fatalf("MustNotExist on present: %v", err)
	}
	if _, err := c.Patch(ctx, "c", "x", map[string]any{"a": int64(2)}, firestore.IfUpdateTime(d.UpdateTime)); err != nil {
		t.Fatalf("fresh updateTime: %v", err)
	}
	if _, err := c.Patch(ctx, "c", "x", map[string]any{"a": int64(3)}, firestore.IfUpdateTime(d.UpdateTime)); !errors.Is(err, firestore.ErrPrecondition) {
		t.Fatalf("stale updateTime: %v", err)
	}
	if v, _ := f.Value("c", "x"); v["a"] != int64(2) {
		t.Fatalf("a stale write changed the document: %v", v)
	}
}

func TestGetAndDelete(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	f.Set("meta", "installation", map[string]any{"project": "p", "version": int64(1)})
	c := newClient(t, f)
	ctx := context.Background()
	d, err := c.Get(ctx, "meta", "installation")
	if err != nil || d.Fields["project"] != "p" || d.Fields["version"] != int64(1) {
		t.Fatalf("get = %+v, %v", d, err)
	}
	if err := c.Delete(ctx, "meta", "installation"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "meta", "installation"); !errors.Is(err, firestore.ErrNotFound) || errors.Is(err, firestore.ErrNoDatabase) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := c.Delete(ctx, "meta", "installation"); err != nil {
		t.Fatalf("deleting an absent document: %v", err)
	}
	if err := c.Delete(ctx, "meta", "installation", firestore.MustExist()); !errors.Is(err, firestore.ErrNotFound) {
		t.Fatalf("MustExist delete: %v", err)
	}
	before := len(f.Requests())
	for _, id := range []string{"", "a/b", "..", "__x__"} {
		if _, err := c.Get(ctx, "meta", id); err == nil || len(f.Requests()) != before {
			t.Errorf("id %q was not refused locally (%v)", id, err)
		}
	}
}

func TestQueryPaging(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	// 7 documents over 3 dates, ids sort differently from dates; two outside the range, one without the field.
	for i, d := range []string{"2026-09-30", "2026-10-01", "2026-10-01", "2026-10-02", "2026-10-02", "2026-10-02", "2026-10-04"} {
		f.Set("spendDaily", fmt.Sprintf("%s_%c", d, 'a'+rune(6-i)), map[string]any{"date": d, "n": int64(i)})
	}
	f.Set("spendDaily", "nodate", map[string]any{"n": int64(99)})
	f.Set("other", "2026-10-01_z", map[string]any{"date": "2026-10-01"})
	c := newClient(t, f, firestore.WithPageSize(2))
	docs, err := c.Query(context.Background(), "spendDaily", "date", "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	var want []string
	for i, d := range []string{"2026-09-30", "2026-10-01", "2026-10-01", "2026-10-02", "2026-10-02", "2026-10-02", "2026-10-04"} {
		if d >= "2026-10-01" && d <= "2026-10-02" {
			want = append(want, fmt.Sprintf("%s_%c", d, 'a'+rune(6-i)))
		}
	}
	sortStrings(want)
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	// 5 results at page size 2 = 3 requests; an exact multiple needs one extra, empty page.
	n := 0
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ":runQuery") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d query requests, want 3", n)
	}
	c4 := newClient(t, f, firestore.WithPageSize(5))
	if docs, err := c4.Query(context.Background(), "spendDaily", "date", "2026-10-01", "2026-10-02"); err != nil || len(docs) != 5 {
		t.Fatalf("exact page multiple: %d docs, %v", len(docs), err)
	}
	// Open bounds, and an empty result.
	if docs, _ := c.Query(context.Background(), "spendDaily", "date", nil, "2026-09-30"); len(docs) != 1 {
		t.Fatalf("open lower bound: %d", len(docs))
	}
	if docs, err := c.Query(context.Background(), "spendDaily", "date", "2027-01-01", nil); err != nil || len(docs) != 0 {
		t.Fatalf("empty: %d, %v", len(docs), err)
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func TestNotFoundIsErrNotFound(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	_, err := c.Get(context.Background(), "spendDaily", "nope")
	var e *firestore.Error
	if !errors.Is(err, firestore.ErrNotFound) || !errors.As(err, &e) || e.Status != 404 || e.Code != "NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
	f.RemoveDatabase()
	for name, call := range map[string]func() error{
		"get":   func() error { _, err := c.Get(context.Background(), "a", "b"); return err },
		"patch": func() error { _, err := c.Patch(context.Background(), "a", "b", map[string]any{"x": "y"}); return err },
		"query": func() error { _, err := c.Query(context.Background(), "a", "date", nil, nil); return err },
		"db":    func() error { _, err := c.GetDatabase(context.Background()); return err },
	} {
		err := call()
		if !errors.Is(err, firestore.ErrNotFound) {
			t.Errorf("%s without a database: %v", name, err)
		}
		if name != "db" && !errors.Is(err, firestore.ErrNoDatabase) {
			t.Errorf("%s without a database is not ErrNoDatabase: %v", name, err)
		}
	}
}

func TestPermissionAndUnavailableTyped(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	ctx := context.Background()
	f.DenyNext(1)
	_, err := c.Patch(ctx, "c", "x", map[string]any{"a": "b"})
	if !errors.Is(err, firestore.ErrPermission) || errors.Is(err, firestore.ErrUnavailable) {
		t.Fatalf("deny: %v", err)
	}
	if _, ok := f.Value("c", "x"); ok {
		t.Fatal("a denied write changed the store")
	}
	f.Refuse(403, "PERMISSION_DENIED", "", "no")
	if _, err := c.Get(ctx, "c", "x"); !errors.Is(err, firestore.ErrPermission) {
		t.Fatalf("403: %v", err)
	}
	for _, code := range []int{503, 500, 429} {
		f.Refuse(code, "UNAVAILABLE", "", "later")
		if _, err := c.Get(ctx, "c", "x"); !errors.Is(err, firestore.ErrUnavailable) {
			t.Fatalf("%d: %v", code, err)
		}
	}
	f.Refuse(409, "ABORTED", "", "too much contention")
	if _, err := c.Get(ctx, "c", "x"); !errors.Is(err, firestore.ErrUnavailable) {
		t.Fatalf("ABORTED: %v", err)
	}
	f.Refuse(400, "INVALID_ARGUMENT", "", "bad")
	_, err = c.Get(ctx, "c", "x")
	for _, s := range []error{firestore.ErrPermission, firestore.ErrUnavailable, firestore.ErrNotFound, firestore.ErrPrecondition} {
		if errors.Is(err, s) {
			t.Fatalf("a plain 400 matched %v", s)
		}
	}
	f.Refuse(0, "", "", "")
	// Unreachable server.
	f.Close()
	if _, err := c.Get(ctx, "c", "x"); !errors.Is(err, firestore.ErrUnavailable) {
		t.Fatalf("closed: %v", err)
	}
}

func TestNoCredentialsInErrors(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	f.Refuse(403, "PERMISSION_DENIED", "", "caller tok-SECRET may not")
	_, err := c.Get(context.Background(), "c", "x")
	if err == nil || strings.Contains(err.Error(), "tok-SECRET") {
		t.Fatalf("err = %v", err)
	}
	f.Close()
	_, err = c.Get(context.Background(), "c", "x")
	if err == nil || strings.Contains(err.Error(), "tok-SECRET") || strings.Contains(err.Error(), "127.0.0.1") && strings.Contains(err.Error(), "?") {
		t.Fatalf("err = %v", err)
	}
	// The token source failing is typed and carries no token.
	bad, _ := firestore.New(f.URL, proj, failingSource{})
	if _, err := bad.Get(context.Background(), "c", "x"); !errors.Is(err, firestore.ErrUnavailable) {
		t.Fatalf("token failure: %v", err)
	}
}

type failingSource struct{}

func (failingSource) Token() (*oauth2.Token, error) { return nil, errors.New("metadata server down") }

func TestAuthAndQuotaHeaders(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	c.Get(context.Background(), "c", "x")
	if got := f.Credentials(); len(got) != 1 || got[0] != "Bearer tok-SECRET" {
		t.Fatalf("auth = %v", got)
	}
	if got := f.QuotaProjects(); got[0] != proj {
		t.Fatalf("quota = %v", got)
	}
	c2 := newClient(t, f, firestore.WithoutQuotaProject())
	c2.Get(context.Background(), "c", "x")
	if got := f.QuotaProjects(); got[1] != "" {
		t.Fatalf("quota = %v", got)
	}
}

func TestContextCancelled(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Get(ctx, "c", "x")
	if !errors.Is(err, context.Canceled) || errors.Is(err, firestore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestDatabaseGetCreate(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	c := newClient(t, f, firestore.WithPollInterval(time.Millisecond))
	ctx := context.Background()
	db, err := c.GetDatabase(ctx)
	if err != nil || db.LocationID != "us-east5" || db.Type != "FIRESTORE_NATIVE" || db.DeleteProtection != "DELETE_PROTECTION_ENABLED" {
		t.Fatalf("db = %+v, %v", db, err)
	}
	if _, err := c.CreateDatabase(ctx, "nam5", true); !errors.Is(err, firestore.ErrPrecondition) {
		t.Fatalf("create over existing: %v", err)
	}
	if loc, _ := f.Database(); loc != "us-east5" {
		t.Fatalf("an existing database changed to %s", loc)
	}
	f.RemoveDatabase()
	if _, err := c.GetDatabase(ctx); !errors.Is(err, firestore.ErrNotFound) {
		t.Fatalf("get absent: %v", err)
	}
	if _, err := c.CreateDatabase(ctx, "mars-1", true); err == nil {
		t.Fatal("a bad location was accepted")
	}
	db, err = c.CreateDatabase(ctx, "us-east5", true)
	if err != nil || db.LocationID != "us-east5" || db.DeleteProtection != "DELETE_PROTECTION_ENABLED" {
		t.Fatalf("created = %+v, %v", db, err)
	}
	// The operation was polled, not assumed done.
	var polled bool
	for _, r := range f.Requests() {
		polled = polled || strings.Contains(r.Path, "/operations/")
	}
	if !polled {
		t.Fatal("the create operation was never polled")
	}
	f.RemoveDatabase()
	f.DenyNext(1)
	if _, err := c.CreateDatabase(ctx, "us-east5", true); !errors.Is(err, firestore.ErrPermission) {
		t.Fatalf("denied create: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "t"})
	for _, tc := range []struct {
		url, project string
		src          oauth2.TokenSource
		ok           bool
	}{
		{"", proj, src, true},
		{"https://firestore.googleapis.com", proj, src, true},
		{"http://127.0.0.1:8080", proj, src, true},
		{"http://localhost:1", proj, src, true},
		{"http://firestore.googleapis.com", proj, src, false},
		{"https://evil.example.com", proj, src, false},
		{"http://evil.example.com", proj, src, false},
		{"https://firestore.googleapis.com/v1", proj, src, false},
		{"https://u:p@firestore.googleapis.com", proj, src, false},
		{"https://firestore.googleapis.com?x=1", proj, src, false},
		{"", "Bad Project", src, false},
		{"", "a/b", src, false},
		{"", proj, nil, false},
	} {
		_, err := firestore.New(tc.url, tc.project, tc.src)
		if (err == nil) != tc.ok {
			t.Errorf("New(%q, %q): %v", tc.url, tc.project, err)
		}
	}
}
