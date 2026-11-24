package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A history image the release behind copied into the registry is Fugaro's own:
// the one `fugaro update` is the whole upgrade, so it moves the tag to this
// release's image with no flag and no terminal answer (the owner's ruling of
// 2026-11-22: a refusal whose fix is a flag Fugaro could pass itself is a bug).
// An image the source never published is another matter and stays refused,
// with the fix printed.
const historyTag = initProject + "/fugaro-base/history:latest"

// ownEarlierRig is the installation one release behind with its registry
// holding the previous release's history image, found by the no-flag update
// that adopts the budget database (the one path of `update` that copies it),
// a run in flight and a policy node one version behind.
func ownEarlierRig(t *testing.T) *convergeRig {
	t.Helper()
	r := newConvergeRig(t, v7Node())
	// The project's config as a machine that never recorded the database
	// holds it: the address is what "known" means, so the no-flag update finds
	// the database and records it.
	file := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cut := strings.Replace(string(b), "rtdb_url: "+r.db.URL+", ", "", 1)
	if cut == string(b) {
		t.Fatalf("no rtdb_url to take out of %s:\n%s", file, b)
	}
	if err := os.WriteFile(file, []byte(cut), 0o644); err != nil {
		t.Fatal(err)
	}
	mayCreate(t, r.fbRig)
	noDatabaseYet(t, r.fbRig)
	updating(t, r.initRig)
	r.src.publishLayer("dimipaun/fugaro-history", convergePrev, "history of "+convergePrev)
	return r
}

// putInRegistry makes the registry hold dst's tag as the amd64 image of the
// source tag, which is what an earlier `fugaro update` copy left there.
func (r *convergeRig) holdAsCopied(t *testing.T, srcTag string) string {
	t.Helper()
	ix := r.src.mans[r.src.tags["dimipaun/fugaro-history:"+srcTag]]
	var idx struct {
		Manifests []struct{ Digest string } `json:"manifests"`
	}
	if err := json.Unmarshal(ix, &idx); err != nil || len(idx.Manifests) != 1 {
		t.Fatalf("source index: %v %s", err, ix)
	}
	d := idx.Manifests[0].Digest
	r.dst.mans[d], r.dst.types[d] = r.src.mans[d], r.src.types[d]
	var im struct {
		Config struct{ Digest string }
		Layers []struct{ Digest string }
	}
	if err := json.Unmarshal(r.src.mans[d], &im); err != nil {
		t.Fatal(err)
	}
	r.dst.blobs[im.Config.Digest] = r.src.blobs[im.Config.Digest]
	for _, l := range im.Layers {
		r.dst.blobs[l.Digest] = r.src.blobs[l.Digest]
	}
	r.dst.tags[historyTag] = d
	r.historyImage()
	return d
}

func TestUpdateReplacesAnEarlierReleasesHistoryImageByItself(t *testing.T) {
	r := ownEarlierRig(t)
	prev := r.holdAsCopied(t, convergePrev)
	run := startRunInFlight(t, r)

	convergeOn(t, r, nil)

	now := r.dst.tags[historyTag]
	if now == prev || now == "" {
		t.Fatalf("history:latest is %q, still the previous release's image %s", now, prev)
	}
	run.laterWrites(t, r)
}

func TestUpdateStillRefusesAHistoryImageFugaroNeverPublished(t *testing.T) {
	r := ownEarlierRig(t)
	other := cdg([]byte("hand pushed history"))
	r.dst.mans[other], r.dst.types[other] = []byte("x"), "application/vnd.oci.image.manifest.v1+json"
	r.dst.tags[historyTag] = other
	currentRelease(t)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "aurora\n", "update")
	if err == nil {
		t.Fatalf("update replaced an image Fugaro never published:\n%s", out)
	}
	msg := ErrorText(err) + out + errOut
	if !strings.Contains(msg, other) || !strings.Contains(msg, "--replace-image history") {
		t.Errorf("the refusal does not print the digest and the fix:\n%s", msg)
	}
	if r.dst.tags[historyTag] != other {
		t.Error("the foreign image was replaced")
	}
}

// `update --repo` takes a checkout's path, and a person passes owner/name: the
// refusal says the fix in one line instead of only that no checkout is there.
func TestUpdateRepoWithOwnerNameSaysTheFix(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, err := executeStdin(t, "", "update", "--repo", "dimipaun/fugaro")
	if err == nil {
		t.Fatal("update --repo owner/name succeeded outside a checkout")
	}
	for _, want := range []string{"not inside a git checkout", "PATH, not owner/name", "--repo alone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no %q in: %v", want, err)
		}
	}
}
