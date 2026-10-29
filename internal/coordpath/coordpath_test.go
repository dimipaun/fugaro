package coordpath

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestCoordpathPathsPinnedToTheirLiterals: the generated rules and the doctor
// check that reads their marker both write and read these strings, so a
// changed literal here moves the coordinator's whole subtree out from under
// the grant that authorises it (D195). The literals are pinned rather than
// derived, so the test fails on the change and not on the drift it causes.
func TestCoordpathPathsPinnedToTheirLiterals(t *testing.T) {
	if PathRoot != "coordinator" {
		t.Errorf("PathRoot = %q, want %q", PathRoot, "coordinator")
	}
	if PathProject != "coordinator/project" {
		t.Errorf("PathProject = %q, want %q", PathProject, "coordinator/project")
	}
	if PathRepos != "coordinator/repos" {
		t.Errorf("PathRepos = %q, want %q", PathRepos, "coordinator/repos")
	}
	slug := "owner/repo-a"
	want := "coordinator/repos/" + budget.Key(slug)
	if got := PathRepo(slug); got != want {
		t.Errorf("PathRepo(%q) = %q, want %q (the slug escaped through budget.Key, as the rules expect)", slug, got, want)
	}
}

// TestCoordpathDocumentPathsPinnedToTheirLiterals pins the two document paths:
// the project-wide one hangs off PathProject, the repository's off that
// repository's own node.
func TestCoordpathDocumentPathsPinnedToTheirLiterals(t *testing.T) {
	slug := "owner/repo-a"
	id := DocumentID("intent", "who", "what")
	if got, want := PathProjectDocument(id), PathProject+"/"+id; got != want {
		t.Errorf("PathProjectDocument(%q) = %q, want %q", id, got, want)
	}
	if got, want := PathRepoDocument(slug, id), "coordinator/repos/"+budget.Key(slug)+"/"+id; got != want {
		t.Errorf("PathRepoDocument(%q, %q) = %q, want %q", slug, id, got, want)
	}
}

// TestCoordpathDocumentIDLengthPrefixesItsParts is the property the ID's whole
// reason rests on: a document's key describes the thing it is about and
// nothing else, so two different descriptions of two different things must not
// collide. Without the length prefix the parts run together and ("a", "bc") and
// ("ab", "c") would be one document.
func TestCoordpathDocumentIDLengthPrefixesItsParts(t *testing.T) {
	if DocumentID("a", "bc") == DocumentID("ab", "c") {
		t.Error("DocumentID(\"a\", \"bc\") == DocumentID(\"ab\", \"c\"): the parts are not length-prefixed, so two different documents collide")
	}
	if DocumentID("a", "bc") != DocumentID("a", "bc") {
		t.Error("DocumentID is not deterministic: the same description twice is not one key")
	}
	if DocumentID("one") == DocumentID("one", "") {
		t.Error("DocumentID(\"one\") == DocumentID(\"one\", \"\"): a trailing empty part is not distinguished")
	}
}

// TestCoordpathDocumentIDIsOneHexSHA256: the answer has to be a Realtime
// Database key, so it is lower-case hex and not base64 — a base64 key can hold
// a slash or a plus, and a hex key holds neither (the same reason budget.Key
// exists).
func TestCoordpathDocumentIDIsOneHexSHA256(t *testing.T) {
	id := DocumentID("project", "view")
	if len(id) != 64 {
		t.Fatalf("DocumentID = %q (%d chars), want 64: the hex of one SHA-256", id, len(id))
	}
	if strings.ToLower(id) != id {
		t.Errorf("DocumentID = %q, want lower-case hex", id)
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("DocumentID = %q holds %q, which is not lower-case hex", id, r)
		}
	}
}
