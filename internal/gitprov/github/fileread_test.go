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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// personReadToken is the token the fake source below mints, and
// testdata/fileread carries it as the request's own Authorization header: a
// read that minted anywhere else fails the replay. It is a fixture value and
// not a credential.
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

// fileReadNowhere is the options for a read that must not be sent anywhere: a
// fixture holding no exchange, which fails the test if a request is made.
func fileReadNowhere(t *testing.T) Options {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(file, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{AppID: "1234", BaseURL: httpfixture.Serve(t, file).URL}
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

// statusOf is the *httpjson.StatusError an answer keeps wrapped: both of
// FileAt's 404 answers carry it, so the status, the URL and GitHub's own
// message are still there for whoever reads the failure.
func statusOf(t *testing.T, err error) *httpjson.StatusError {
	t.Helper()
	var se *httpjson.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want the reply's own status kept in it", err)
	}
	return se
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
	if se := statusOf(t, err); se.Status != http.StatusNotFound {
		t.Fatalf("the wrapped status is %d, want the reply's own 404", se.Status)
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

// TestFileAtRefusesARefTheRepositoryDoesNotHave: the contents API answers a
// path it does not carry and a ref it cannot resolve with the same 404, and
// they are two different rows of the design's own failure table (spec.md §7):
// a document that is not there selects nothing and leaves the run on the
// repository's own job, while a deleted, force-pushed or mistyped ref is a
// refusal naming the ref (spec.md §3). A read that read the second as the
// first would place the run by a document nobody ever read, and fail later at
// clone — the fallback D905 refuses.
func TestFileAtRefusesARefTheRepositoryDoesNotHave(t *testing.T) {
	_, err := FileAt(ctx, fileReadOptions(t, "missing_ref.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("a ref the repository does not have: err = %v, want ErrRefNotFound", err)
	}
	if errors.Is(err, ErrFileNotFound) {
		t.Fatalf("a ref the repository does not have: err = %v, want it never to be read as the absent-file answer", err)
	}
	if !strings.Contains(err.Error(), "release/1.2") {
		t.Fatalf("err = %v, want it to name the ref", err)
	}
	if se := statusOf(t, err); se.Status != http.StatusNotFound {
		t.Fatalf("the wrapped status is %d, want the reply's own 404", se.Status)
	}
}

// TestFileAtRefusesA404ForARepositoryItCannotReach: "Not Found" is what the
// contents API answers for a path the ref does not carry *and* for a
// repository this token cannot reach — a name that is not the installation's
// repository, one outside the App's scope, one deleted after the mint, or a
// token minted for another repository, which nothing here binds it to. The
// second is spec.md §7's row for a repository the installation does not hold,
// and it is a refusal; taking it for the absent-file row would place the run
// on the per-repository job by a document nobody read. So the answer is taken
// only where the repository is confirmed, and a confirmation that 404s is a
// refusal — neither sentinel.
func TestFileAtRefusesA404ForARepositoryItCannotReach(t *testing.T) {
	_, err := FileAt(ctx, fileReadOptions(t, "repo_missing.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if err == nil {
		t.Fatal("a 404 for the repository itself was read as the absent-file answer")
	}
	if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want neither typed answer: the repository is not one this read can reach", err)
	}
	if !strings.Contains(err.Error(), "/repos/acme/web") {
		t.Fatalf("err = %v, want the refusal to name the repository it could not confirm", err)
	}
	if se := statusOf(t, err); se.Status != http.StatusNotFound {
		t.Fatalf("the wrapped status is %d, want the confirmation's own 404", se.Status)
	}
}

// TestFileAtRefusesA404WhereTheRefDoesNotResolve: the other half of the same
// ambiguity. The read's own body says "Not Found" — a wording that would put a
// missing ref in the absent-file row if GitHub ever reworded the one that names
// the ref — so the ref is confirmed too, and a confirmation that does not
// resolve it is a refusal.
func TestFileAtRefusesA404WhereTheRefDoesNotResolve(t *testing.T) {
	_, err := FileAt(ctx, fileReadOptions(t, "ref_gone.json"), &fakePersonReadTokens{}, "acme/web", "release/1.2", "fugaro.yaml")
	if err == nil {
		t.Fatal("a 404 for a ref that does not resolve was read as the absent-file answer")
	}
	if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want neither typed answer: the ref could not be confirmed", err)
	}
	if se := statusOf(t, err); se.Status != http.StatusNotFound {
		t.Fatalf("the wrapped status is %d, want the confirmation's own 404", se.Status)
	}
}

// TestFileAtConfirmsTheRepositoryAloneWhenNoRefIsNamed: a read that names no
// ref is answered about the API's own default branch, which is one the
// repository has, so there is no ref to confirm — and the fixture holds exactly
// the two exchanges this read may make, so a second confirmation, or one sent
// anywhere else, fails the replay instead of passing quietly. What the answer
// still costs is the repository confirmation: `metadata: read` on its own.
func TestFileAtConfirmsTheRepositoryAloneWhenNoRefIsNamed(t *testing.T) {
	_, err := FileAt(ctx, fileReadOptions(t, "default_branch.json"), &fakePersonReadTokens{}, "acme/web", "", "fugaro.yaml")
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("a file the repository does not hold at its default branch: err = %v, want ErrFileNotFound", err)
	}
	if !strings.Contains(err.Error(), "the default branch") {
		t.Fatalf("err = %v, want it to name the default branch rather than an empty ref", err)
	}
	if se := statusOf(t, err); se.Status != http.StatusNotFound {
		t.Fatalf("the wrapped status is %d, want the read's own 404", se.Status)
	}
}

// TestFileAtRefusesA404ItCannotRead: a 404 is one of the two typed answers
// only when its own body says which — a bare "Not Found" for the path, "No
// commit found …" for the ref — and anything else is Contents's own refusal.
// The other reading is the one D905 forbids: an answer this code cannot
// classify, read as the absent-file row, places the run on the per-repository
// job by a document nobody read. Both shapes are here: a body that is not the
// API's answer object at all ("not found", which is what a proxy or a GHES in
// front of it sends) and a JSON answer reworded past recognition ("Not
// Found.").
func TestFileAtRefusesA404ItCannotRead(t *testing.T) {
	for _, fixture := range []string{"unreadable.json", "reworded.json"} {
		ts := &fakePersonReadTokens{}
		_, err := FileAt(ctx, fileReadOptions(t, fixture), ts, "acme/web", "release/1.2", "fugaro.yaml")
		if err == nil {
			t.Fatalf("%s: a 404 this code cannot read was accepted", fixture)
		}
		if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrRefNotFound) {
			t.Fatalf("%s: err = %v, want neither typed answer: the body does not say which one it is", fixture, err)
		}
		if !strings.Contains(err.Error(), "reading fugaro.yaml") {
			t.Fatalf("%s: err = %v, want Contents's own wording kept", fixture, err)
		}
		if se := statusOf(t, err); se.Status != http.StatusNotFound {
			t.Fatalf("%s: the wrapped status is %d, want the reply's own 404", fixture, se.Status)
		}
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
// nothing else, refused before anything is sent — the fixture holds no
// exchange, so a name that reached a request would fail this test rather than
// be read as a path.
func TestFileAtRefusesARepositoryItCannotName(t *testing.T) {
	for _, repo := range []string{"web", "", "acme/", "/web", "acme/web/extra", "acme//web"} {
		_, err := FileAt(ctx, fileReadNowhere(t), &fakePersonReadTokens{}, repo, "release/1.2", "fugaro.yaml")
		if err == nil {
			t.Fatalf("FileAt for %q was accepted", repo)
		}
		if !strings.Contains(err.Error(), "owner/name") {
			t.Fatalf("FileAt for %q: err = %v, want it to name the shape", repo, err)
		}
	}
}
