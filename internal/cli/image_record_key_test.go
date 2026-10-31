package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// theOperatorsKey is a key of the shape Key returns — 64 lower-case hex
// characters — and not one this checkout's files reduce to: what the test is
// about is that the record carries the value it was handed, and a build that
// worked one out from what it cloned would write something else here.
const theOperatorsKey = "5a1c9e0d3b7f42c6a8e1d0b9c8f7e6d5a4b3c2d1e0f9a8b7c6d5e4f3a2b1c0d9"

// TestRecordKeyIsTheOperatorsValue: D622. The key is computed on the
// operator's machine (imagecheck.Key, from the key files it read) and handed
// to the build as an input, and the build records it verbatim: render writes
// it into record.json, record completes that record and writes it to the
// bucket, and neither recomputes one. A build that did compute its own would
// be keying from a clone nobody chose — and the image the lock then names by
// that key is one no other repository would ever be sent to.
func TestRecordKeyIsTheOperatorsValue(t *testing.T) {
	checkoutWith(t, npmFiles())
	testutil.Git(t, ".", "remote", "set-url", "origin", "https://github.com/acme/app.git")
	slug := mustSlug("bitbucket", "acme/app")
	bucket := "file://" + t.TempDir()
	ws := t.TempDir()

	if _, stderr, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", ws, "--key", theOperatorsKey); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	rec := readRenderedRecord(t, ws)
	if rec.Key != theOperatorsKey {
		t.Errorf("the rendered record's key = %q, want the operator's %q", rec.Key, theOperatorsKey)
	}

	// The record step completes that record and writes it where a launch
	// reads it, and the key is the same value there.
	testutil.WriteFiles(t, ws, map[string]string{
		"image-digest": "sha256:" + strings.Repeat("1", 64) + "\n",
		"base-digest":  "b@sha256:" + strings.Repeat("2", 64) + "\n",
	})
	if _, _, err := execute(t, "image", "gate", "--record", filepath.Join(ws, "record.json"), "--slug", slug, "--bucket", bucket); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "image", "record", "--in", filepath.Join(ws, "record.json"), "--digest-from", filepath.Join(ws, "image-digest"),
		"--base-digest-from", filepath.Join(ws, "base-digest"), "--image", appImage(), "--base", "b:1", "--build-id", "b0001",
		"--slug", slug, "--bucket", bucket); err != nil {
		t.Fatal(err)
	}
	stored := readImageRecord(t, bucket)
	if stored == nil {
		t.Fatal("the record step wrote no record")
	}
	if stored.Key != theOperatorsKey {
		t.Errorf("the recorded key = %q, want the operator's %q", stored.Key, theOperatorsKey)
	}

	// A build handed no key writes a record with no key at all: an absent
	// field, never one computed in the build.
	plain := t.TempDir()
	if _, stderr, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", plain); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	data, err := os.ReadFile(filepath.Join(plain, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if v, ok := fields["key"]; ok {
		t.Errorf("a build handed no key recorded one of its own: %s", v)
	}
}

// TestRecordRefusesAValueThatIsNotAKey: the key is the operator's and the
// build records it verbatim (D622), so a value that is not a key must not
// reach the record: every later launch chooses the image by that field, and a
// record holding something else would name an image no build made.
func TestRecordRefusesAValueThatIsNotAKey(t *testing.T) {
	checkoutWith(t, npmFiles())
	for _, bad := range []string{
		strings.ToUpper(theOperatorsKey),
		theOperatorsKey + "0",
		theOperatorsKey[:63],
		strings.Repeat("g", 64),
		"sha256:" + theOperatorsKey,
	} {
		ws := t.TempDir()
		_, _, err := execute(t, "image", "render", "--workflow", "app", "--cloud-outputs", ws, "--key", bad)
		if err == nil {
			t.Errorf("render accepted the key %q", bad)
			continue
		}
		if !strings.Contains(err.Error(), "--key") {
			t.Errorf("render refused %q with %q, which does not say which flag it was", bad, err)
		}
		if _, err := os.Stat(filepath.Join(ws, "record.json")); err == nil {
			t.Errorf("render wrote a record for the key %q", bad)
		}
	}
}

// readRenderedRecord is the record.json render wrote into the build's
// workspace.
func readRenderedRecord(t *testing.T, ws string) imagecheck.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec imagecheck.Record
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}
