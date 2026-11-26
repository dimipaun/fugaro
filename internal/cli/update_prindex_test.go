package cli

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	roleaccess "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// update_prindex_test.go is D3204's retirement of the bucket's pull-request
// index as the CLI's own writer (IR5b of
// docs/console-fixes/implementation-plan.md). The step wrote one object per run
// that names a pull request under runs/<slug>/pr-index/ (D2965), and the launch
// wrote one for a --pr launch of its own (RL2W); `runs ls --pr N` reads the live
// index now, so the writers stopped and what the update must do with the objects
// a run already left is nothing at all: write none, rewrite none, take none away
// — a reader still falls back to them (runs_pr.go), which is why they stay where
// they are.
//
// The fixtures here are shared with update_summary_test.go — the rig, the
// bucket, the run planter and the tree readers — the two steps being the two
// halves of the same tail of the update: each writes a derived object over an
// installation's history, and neither may touch what a run holds.

// sevenPR is the pull request the fixture's runs are on.
const sevenPR = 7

// prIndexRig is the update door's own fixture — an installation one release
// behind, whose runs bucket this test can plant runs in and read back.
func prIndexRig(t *testing.T) *convergeRig {
	t.Helper()
	return newConvergeRig(t, currentNode())
}

// prIndexBucket is the installation's runs bucket, opened the way a step that
// writes an entry would open it (sharedBucketOpener's URL): the objects planted
// here are the objects the update walks.
func prIndexBucket(t *testing.T, r *convergeRig) *blob.Bucket {
	t.Helper()
	b, err := blobx.Open(t.Context(), r.localConfig(t).BucketURL())
	if err != nil {
		t.Fatalf("opening the rig's runs bucket: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b.Bucket
}

// prIndexViewer is the credential the listings below are made under: the row
// reader drops every run of a repository the caller holds no role in, so a
// listing a test asserts against has to name a member.
const prIndexViewer = "user:test@example.com"

// lsAsViewer makes the fixture's credential a viewer of the project, for the
// `runs ls` below and no other reason. The grants node is planted rather than
// written through the writer because these tests are about the index and not
// about the roles; an update is free to move the node, which is why every
// listing plants it again.
func lsAsViewer(t *testing.T, r *convergeRig) {
	t.Helper()
	accessRunAs(t, prIndexViewer)
	r.db.Set(roleaccess.PathNode, accessNodeDoc(projectGrant(prIndexViewer, roleaccess.RoleViewer)))
}

// plantRun stores one planted run: its task, its record, or both, the way the
// launcher and the runner write them.
func plantRun(t *testing.T, b *blob.Bucket, slug, id string, spec *task.Spec, rec *runstore.Record) {
	t.Helper()
	s := runstore.Open(b, slug, id)
	if spec != nil {
		if err := s.CreateTask(t.Context(), spec); err != nil {
			t.Fatalf("planting %s's task: %v", id, err)
		}
	}
	if rec != nil {
		if err := s.WriteRecord(t.Context(), rec); err != nil {
			t.Fatalf("planting %s's record: %v", id, err)
		}
	}
}

// prIndexKeys is the object names under one prefix of the bucket whose leaf is
// an index entry.
func prIndexKeys(t *testing.T, b *blob.Bucket, prefix string) []string {
	t.Helper()
	var out []string
	for key := range runsTree(t, b, prefix) {
		if strings.Contains(key, "/"+runstore.PRIndexDir+"/") {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}

// runsTree is every object under one prefix of the bucket, key to bytes. Taken
// before and after a run it is how a test says "nothing moved" about a whole
// subtree — the entries a step writes included — rather than about the objects
// it happened to think of.
func runsTree(t *testing.T, b *blob.Bucket, prefix string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	it := b.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := it.Next(t.Context())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("listing %s: %v", prefix, err)
		}
		if obj.IsDir {
			continue
		}
		data, err := b.ReadAll(t.Context(), obj.Key)
		if err != nil {
			t.Fatalf("reading %s: %v", obj.Key, err)
		}
		out[obj.Key] = data
	}
	return out
}

// sameTree fails the test for every object that appeared, vanished or changed
// between two readings of one subtree.
func sameTree(t *testing.T, what string, before, after map[string][]byte) {
	t.Helper()
	for key, was := range before {
		now, ok := after[key]
		switch {
		case !ok:
			t.Errorf("%s removed %s: a step of the update creates what is missing and never takes away what a run holds", what, key)
		case string(now) != string(was):
			t.Errorf("%s rewrote %s:\n  was %s\n  now %s", what, key, was, now)
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			t.Errorf("%s wrote %s, which was not there before", what, key)
		}
	}
}

// lsRunIDs is `runs ls` under the fixture's own credential, as the run IDs it
// lists, sorted. It is what a person reads, so it is what the rows the index has
// to agree with are taken from.
func lsRunIDs(t *testing.T, args ...string) []string {
	t.Helper()
	got, _ := lsJSON(t, args...)
	out := make([]string, 0, len(got.Runs))
	for _, row := range got.Runs {
		out = append(out, row.RunID)
	}
	slices.Sort(out)
	return out
}

// TestUpdateSkipsPRIndexBackfill: `fugaro update` writes nothing under
// runs/<slug>/pr-index/, and says nothing about it. The two runs of the fixture
// are the shapes D2965's walk wrote an entry for — a run whose record names the
// pull request it opened, and a follow-up whose task names the one it continued
// — so a step of the update that backfilled the index would write two objects
// here and print its line.
//
// The entry the installation already holds is the half D3204 keeps: the objects
// are left in place, so the update neither rewrites nor removes it, and a reader
// that falls back to it still reads the run it names.
//
// Mutation caught: backfillPRIndex wired back into the converge's tail — the two
// entries appear and the update's own line with them.
func TestUpdateSkipsPRIndexBackfill(t *testing.T) {
	r := prIndexRig(t)
	b := prIndexBucket(t, r)
	root := runIDAt(30, "090000", "a1a1")
	fu := runIDAt(0, "000100", "b2b2")
	plantRun(t, b, appSlug, root, firstRunSpec(root), prRecord(root, "", sevenPR, 2))
	plantRun(t, b, appSlug, fu, followUpSpec(fu, root, root, sevenPR), prRecord(fu, "", sevenPR, 1))

	// One entry, written by the release that wrote them, as an installation
	// nobody skipped an upgrade on holds it.
	held, err := runstore.PRIndexPath(appSlug, sevenPR, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runstore.PutPRIndex(t.Context(), b, runstore.PRIndexEntry{
		RunID: root, Slug: appSlug, PR: sevenPR, Kind: runstore.PRIndexOpened, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	prefix := "runs/" + appSlug + "/" + runstore.PRIndexDir + "/"
	before := runsTree(t, b, prefix)
	if len(before) != 1 {
		t.Fatalf("the fixture's index holds %v, want the entry the installation already had: the comparison below is about what an update adds to it", before)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if strings.Contains(out, "pull-request index") {
		t.Errorf("the update still backfills the pull-request index:\n%s", out)
	}
	sameTree(t, "the update", before, runsTree(t, b, prefix))
	if keys := prIndexKeys(t, b, "runs/"+appSlug+"/"); !slices.Equal(keys, []string{held}) {
		t.Errorf("the index holds %v, want the one entry the installation already had (%s)", keys, held)
	}
	// And the objects stay in place for the reader that falls back to them.
	lsAsViewer(t, r)
	if ids := lsRunIDs(t, "--pr", strconv.Itoa(sevenPR), "--repo", "acme/app"); !slices.Contains(ids, root) {
		t.Errorf("runs ls --pr %d no longer lists %s (%v): the entry the update left is what names it", sevenPR, root, ids)
	}
}

// updateStepEngine is a step fixture without the call: the engine, and whether
// the step reached the runs bucket at all. A test that has to say something
// about the run the step is reached by rather than about the step alone — the
// door, the preview, the session, the gate — takes the engine, sets that, and
// calls the step itself (update_summary_test.go's fence test).
func updateStepEngine(t *testing.T) (e *initEngine, opened *bool) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	lc := &localcfg.Config{
		Version: 1, Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		RunsBucket: "fugaro-runs-" + initProject,
		Bucket:     "file://" + t.TempDir(),
		Budget:     &localcfg.Budget{Mode: "enforce", PerRunUSD: 5, RTDBURL: db.URL, FirebaseProject: initProject},
		Endpoints:  localcfg.Endpoints{NoAuth: true},
	}
	touched := false
	prev := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, url string) (*blobx.Bucket, error) {
		touched = true
		return blobx.Open(ctx, url)
	}
	t.Cleanup(func() { sharedBucketOpener = prev })

	return &initEngine{r: &initRun{o: &initOptions{door: updateDoor.name}, w: &strings.Builder{}}, lc: lc}, &touched
}
