package github

// A1's tests (D910, D911): the launcher's reader, one file of one repository at
// one ref, read with the token source it is handed and with no key of this
// process's. Everything here is hermetic — the API is testdata/fileread
// replayed by httpfixture, and the token source is a fake — because the live
// path needs a real person's mint (A3's end-to-end read) and none of this
// waits on it.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// personReadToken is the token the fake source below mints, and testdata
// /fileread carries it as the request's own Authorization header: a read that
// minted anywhere else fails the replay. It is a fixture value and not a
// credential.
const personReadToken = "ghs_fixture_person_read"

// fileReadDocument is the fugaro.yaml testdata/fileread/at_ref.json answers
// with, base64-wrapped there the way GitHub wraps one.
const fileReadDocument = "workflows:\n  default:\n    checkout: clone\n    job_profile: standard\n"

// fakePersonReadTokens is a gitprov.TokenSource a test hands FileAt: it mints
// the token the fixture asserts the request carried, and it counts the asks,
// so a read that minted more than the one call it makes can be seen.
type fakePersonReadTokens struct {
	calls int
}

func (f *fakePersonReadTokens) Token(context.Context, time.Duration) (string, time.Time, error) {
	f.calls++
	return personReadToken, time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC), nil
}

// fileReadOptions is what a launcher hands FileAt besides the repository: the
// values that are not the repository's own, with the API root pointed at the
// fixture. It holds no key, which is the point — a provider built from these
// can only mint through the source it is given.
func fileReadOptions(t *testing.T, fixture string) Options {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", "fileread", fixture))
	return Options{AppID: "1234", BaseURL: srv.URL}
}

// TestFileAtAsksForTheRefItWasGiven: the read is of the repository and the ref
// the caller named, through the contents API the package already has.
// at_ref.json answers one request and only that one — the repository's own
// path with "?ref=release/1.2" — so a reader that took the default branch, the
// ref from somewhere else, or the file out of a working tree cannot pass it.
func TestFileAtAsksForTheRefItWasGiven(t *testing.T) {
	got, err := FileAt(ctx, fileReadOptions(t, "at_ref.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if err != nil {
		t.Fatalf("FileAt: %v", err)
	}
	if string(got) != fileReadDocument {
		t.Fatalf("FileAt = %q, want %q", got, fileReadDocument)
	}
}

// TestFileAtDistinguishesMissingFromRefused: a file the repository does not
// hold at the ref is a typed answer of its own, because the launcher's two
// messages differ for the two — and every other failure stays the refusal
// Contents already words, never a not-found the caller would read as one.
func TestFileAtDistinguishesMissingFromRefused(t *testing.T) {
	_, err := FileAt(ctx, fileReadOptions(t, "missing.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("a file the repository does not hold: err = %v, want ErrFileNotFound", err)
	}
	if !strings.Contains(err.Error(), "fugaro.yaml") || !strings.Contains(err.Error(), "release/1.2") {
		t.Fatalf("err = %v, want it to name the file and the ref", err)
	}
	_, err = FileAt(ctx, fileReadOptions(t, "refused.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if err == nil {
		t.Fatal("a refused read was accepted")
	}
	if errors.Is(err, ErrFileNotFound) {
		t.Fatalf("a refused read: err = %v, want Contents's own refusal", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want Contents's own wording for the refusal", err)
	}
}

// TestFileAtUsesTheTokenSourceItIsGiven: the credential is the source the
// caller handed over and nothing else. The fixture's exchange asserts the
// request's Authorization header, so a read that minted in-job, sent an empty
// token or reached for a key cannot pass it; the count is that one read is one
// mint.
func TestFileAtUsesTheTokenSourceItIsGiven(t *testing.T) {
	ts := &fakePersonReadTokens{}
	if _, err := FileAt(ctx, fileReadOptions(t, "at_ref.json"), ts, "acme/web", "release/1.2", "fugaro.yaml"); err != nil {
		t.Fatalf("FileAt: %v", err)
	}
	if ts.calls != 1 {
		t.Fatalf("the token source was asked %d times, want 1", ts.calls)
	}
}

// TestFileAtRefusesARepositoryItCannotName: repo is one "owner/name" and
// nothing else, refused before anything is sent. The fixture answers one
// exchange that this test must leave untouched: FileAt takes the repository
// apart itself, so a name it cannot take apart is never passed on as a path.
func TestFileAtRefusesARepositoryItCannotName(t *testing.T) {
	for _, repo := range []string{"web", "", "acme/", "/web", "acme/web/extra", "acme//web"} {
		_, err := FileAt(ctx, fileReadOptions(t, "at_ref.json"), &fakePersonReadTokens{}, repo, "release/1.2", "fugaro.yaml")
		if err == nil {
			t.Fatalf("FileAt for %q was accepted", repo)
		}
		if !strings.Contains(err.Error(), "owner/name") {
			t.Fatalf("FileAt for %q: err = %v, want it to name the shape", repo, err)
		}
	}
}
