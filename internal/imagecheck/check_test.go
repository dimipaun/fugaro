package imagecheck

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/config"
)

// mapTree is a Tree over a map of files.
type mapTree map[string]string

func (m mapTree) BlobID(path string) (string, error) {
	data, ok := m[path]
	if !ok {
		return "", fs.ErrNotExist
	}
	return gitBlobID(data), nil
}

func (m mapTree) Glob(pattern string) ([]string, error) {
	var out []string
	for _, p := range slices.Sorted(maps.Keys(m)) {
		if ok, err := doublestar.Match(pattern, p); err != nil {
			return nil, err
		} else if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m mapTree) ReadFile(path string) ([]byte, error) {
	data, ok := m[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(data), nil
}

func (m mapTree) with(path, data string) mapTree {
	out := maps.Clone(m)
	out[path] = data
	return out
}

var (
	digestA      = "sha256:" + strings.Repeat("a", 64)
	digestB      = "sha256:" + strings.Repeat("b", 64)
	digestLatest = "sha256:" + strings.Repeat("c", 64)
	digestOther  = "sha256:" + strings.Repeat("d", 64)
)

var nodeTree = mapTree{
	"package.json":      `{"name":"web"}`,
	"package-lock.json": `{"lockfileVersion":3}`,
	"src/index.ts":      "export {}\n",
}

// baseline is a record of tree built at commit c1 and the inputs of a
// check that finds nothing changed.
func baseline(t *testing.T, now time.Time) Inputs {
	t.Helper()
	cfg := parse(t, webYAML)
	keys, err := KeyFiles(cfg, "web", nodeTree)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ImageConfigHash(cfg, "web", nodeTree)
	if err != nil {
		t.Fatal(err)
	}
	rec := &Record{
		Version: RecordVersion, Repo: "acme/webapp", Workflow: "web", BuiltAt: now.Add(-24 * time.Hour), BuildID: "b1",
		SourceCommit: "c1", KeyFiles: keys, ImageConfigHash: hash,
		BaseRef: "base:1", BaseDigest: digestA, ImageDigest: digestLatest, TemplateSalt: "salt",
	}
	return Inputs{
		Config: cfg, Workflow: "web", Record: rec, Head: "c1", Tree: nodeTree, BuiltCommitReachable: true,
		Changed: func(string, string, []string) (bool, []string, error) { return false, nil, nil },
		BaseRef: "base:1", BaseDigest: digestA, LatestDigest: digestLatest, TemplateSalt: "salt",
		Now: now, Rebuild: cfg.Workflows["web"].Rebuild.Defaults(),
	}
}

func TestDecideTriggers(t *testing.T) {
	now := time.Date(2026, 9, 29, 5, 30, 0, 0, time.UTC)
	off := false
	cases := []struct {
		name     string
		mod      func(t *testing.T, in *Inputs)
		decision string
		reasons  []string
	}{
		{"nothing changed", func(*testing.T, *Inputs) {}, Skip, nil},
		{"force", func(_ *testing.T, in *Inputs) { in.Force = true }, Rebuild, []string{ReasonForce}},
		{"no record", func(_ *testing.T, in *Inputs) { in.Record = nil }, Rebuild, []string{ReasonNoRecord}},
		{"image config", func(t *testing.T, in *Inputs) {
			in.Config = parse(t, strings.Replace(webYAML, "base: web-node,", "base: web-node, image: { apt: [jq] },", 1))
		}, Rebuild, []string{ReasonImageConfig}},
		{"template salt", func(_ *testing.T, in *Inputs) { in.TemplateSalt = "new-salt" }, Rebuild, []string{ReasonImageConfig}},
		{"lockfiles", func(_ *testing.T, in *Inputs) {
			in.Tree = nodeTree.with("package-lock.json", `{"lockfileVersion":3,"x":1}`)
		}, Rebuild, []string{ReasonLockfiles}},
		{"lockfiles off", func(_ *testing.T, in *Inputs) {
			in.Tree = nodeTree.with("package-lock.json", `{"lockfileVersion":3,"x":1}`)
			in.Rebuild.Lockfiles = &off
		}, Skip, nil},
		{"base digest", func(_ *testing.T, in *Inputs) { in.BaseDigest = digestB }, Rebuild, []string{ReasonBase}},
		{"base ref", func(_ *testing.T, in *Inputs) { in.BaseRef = "base:2" }, Rebuild, []string{ReasonBase}},
		{"base off", func(_ *testing.T, in *Inputs) { in.BaseDigest = digestB; in.Rebuild.Base = &off }, Skip, nil},
		{"base unknown", func(_ *testing.T, in *Inputs) { in.BaseDigest = "" }, Skip, []string{ReasonBaseUnknown}},
		{"paths", func(_ *testing.T, in *Inputs) {
			in.Head = "c2"
			in.Rebuild.Paths = []string{"packages/**"}
			in.Changed = func(from, to string, globs []string) (bool, []string, error) {
				if from != "c1" || to != "c2" || !slices.Equal(globs, []string{"packages/**"}) {
					return false, nil, errors.New("wrong arguments")
				}
				return true, []string{"packages/a/src/x.ts"}, nil
			}
		}, Rebuild, []string{ReasonPaths}},
		{"paths not asked", func(_ *testing.T, in *Inputs) {
			in.Head = "c2"
			in.Changed = func(string, string, []string) (bool, []string, error) { return false, nil, errors.New("called") }
		}, Skip, nil},
		{"max age", func(_ *testing.T, in *Inputs) { in.Record.BuiltAt = now.Add(-15 * 24 * time.Hour) }, Rebuild, []string{ReasonMaxAge}},
		{"max age off", func(_ *testing.T, in *Inputs) {
			in.Record.BuiltAt = now.Add(-15 * 24 * time.Hour)
			in.Rebuild.MaxAge = config.Duration{Set: true}
		}, Skip, nil},
		{"built commit gone", func(_ *testing.T, in *Inputs) { in.Head = "c2"; in.BuiltCommitReachable = false }, Rebuild, []string{ReasonBuiltCommitGone}},
		{"latest drift", func(_ *testing.T, in *Inputs) { in.LatestDigest = digestOther }, Rebuild, []string{ReasonLatestDrift}},
		{"latest missing", func(_ *testing.T, in *Inputs) { in.LatestDigest = ""; in.LatestMissing = true }, Rebuild, []string{ReasonLatestDrift}},
		{"latest unknown", func(_ *testing.T, in *Inputs) { in.LatestDigest = "" }, Skip, []string{ReasonLatestUnknown}},
		{"two triggers", func(_ *testing.T, in *Inputs) { in.BaseDigest = digestB; in.LatestDigest = digestOther }, Rebuild, []string{ReasonBase, ReasonLatestDrift}},
		{"force and no record", func(_ *testing.T, in *Inputs) { in.Force = true; in.Record = nil }, Rebuild, []string{ReasonForce, ReasonNoRecord}},
		{"a trigger that can't be evaluated", func(_ *testing.T, in *Inputs) {
			in.Head = "c2"
			in.Rebuild.Paths = []string{"packages/**"}
			in.Changed = func(string, string, []string) (bool, []string, error) {
				return false, nil, errors.New("git diff failed")
			}
		}, CheckFailed, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := baseline(t, now)
			c.mod(t, &in)
			d := Decide(context.Background(), in)
			if d.Decision != c.decision || !slices.Equal(d.Reasons, c.reasons) {
				t.Fatalf("Decide = %s %v (error %q), want %s %v", d.Decision, d.Reasons, d.Error, c.decision, c.reasons)
			}
			if d.Workflow != "web" {
				t.Errorf("workflow = %q", d.Workflow)
			}
			if c.decision == CheckFailed && d.Error == "" {
				t.Error("a failed check has no error")
			}
		})
	}
}

// TestDecideFingerprint: the inputs a build would run with are the head,
// the image config hash, the key files and the base digest of this check.
func TestDecideFingerprint(t *testing.T) {
	in := baseline(t, time.Now())
	in.Head, in.BaseDigest = "c9", digestB
	d := Decide(context.Background(), in)
	want := InputsFingerprint{SourceCommit: "c9", ImageConfigHash: in.Record.ImageConfigHash, KeyFiles: in.Record.KeyFiles, BaseDigest: digestB}
	if !d.Inputs.Equal(want) {
		t.Fatalf("fingerprint = %+v, want %+v", d.Inputs, want)
	}
}

// TestCheckBacksOffAfterFailure: a build that failed with the same inputs
// is not paid for again until something changes, or the check is forced.
func TestCheckBacksOffAfterFailure(t *testing.T) {
	now := time.Now()
	for _, status := range []string{"FAILURE", "TIMEOUT", "INTERNAL_ERROR"} {
		t.Run(status, func(t *testing.T) {
			in := baseline(t, now)
			in.BaseDigest = digestB // the base moved: a rebuild is due
			first := Decide(context.Background(), in)
			if first.Decision != Rebuild {
				t.Fatalf("first = %+v", first)
			}
			in.LastCheck = &CheckState{Decision: Rebuild, BuildID: "b2", BuildInputs: &first.Inputs}
			in.LastBuildStatus = status
			if d := Decide(context.Background(), in); d.Decision != RebuildFailedLast || !slices.Equal(d.Reasons, []string{ReasonBase}) {
				t.Fatalf("same inputs after %s = %+v", status, d)
			}

			// A changed input (a new commit) is worth a new build.
			changed := in
			changed.Head = "c2"
			if d := Decide(context.Background(), changed); d.Decision != Rebuild {
				t.Fatalf("changed inputs = %+v", d)
			}
			forced := in
			forced.Force = true
			if d := Decide(context.Background(), forced); d.Decision != Rebuild || !slices.Contains(d.Reasons, ReasonForce) {
				t.Fatalf("forced = %+v", d)
			}
		})
	}
	// A build that succeeded, or was cancelled, is no reason to back off.
	for _, status := range []string{"SUCCESS", "CANCELLED", ""} {
		in := baseline(t, now)
		in.BaseDigest = digestB
		fp := Decide(context.Background(), in).Inputs
		in.LastCheck = &CheckState{Decision: Rebuild, BuildID: "b2", BuildInputs: &fp}
		in.LastBuildStatus = status
		if d := Decide(context.Background(), in); d.Decision != Rebuild {
			t.Errorf("after %q: %+v", status, d)
		}
	}
}

// TestDecideWaitsForRunningBuild: a build of the same inputs that hasn't
// finished is not submitted a second time.
func TestDecideWaitsForRunningBuild(t *testing.T) {
	in := baseline(t, time.Now())
	in.BaseDigest = digestB
	fp := Decide(context.Background(), in).Inputs
	in.LastCheck = &CheckState{Decision: Rebuild, BuildID: "b2", BuildInputs: &fp}
	in.LastBuildStatus = "WORKING"
	if d := Decide(context.Background(), in); d.Decision != Skip || !slices.Contains(d.Reasons, ReasonBuildRunning) {
		t.Fatalf("Decide = %+v", d)
	}
}

func TestLatestDriftTrigger(t *testing.T) {
	in := baseline(t, time.Now())
	if d := Decide(context.Background(), in); d.Decision != Skip {
		t.Fatalf("latest at the record's digest: %+v", d)
	}
	// Another build promoted latest after this record was written (a
	// promote race): the record no longer describes what runs.
	in.LatestDigest = digestOther
	if d := Decide(context.Background(), in); d.Decision != Rebuild || !slices.Equal(d.Reasons, []string{ReasonLatestDrift}) {
		t.Fatalf("drifted latest: %+v", d)
	}
}

func TestMatchPaths(t *testing.T) {
	got := MatchPaths([]string{"packages/**", "docs/*.md"}, []string{"README.md", "packages/a/src/x.ts", "docs/a.md", "docs/sub/b.md"})
	if !slices.Equal(got, []string{"packages/a/src/x.ts", "docs/a.md"}) {
		t.Fatalf("MatchPaths = %v", got)
	}
}

func TestCheckStateRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 29, 5, 30, 0, 0, time.UTC)
	s := CheckState{Version: CheckVersion, CheckedAt: at, Decision: Rebuild, Reasons: []string{ReasonBase}, BuildID: "b1",
		BuildInputs: &InputsFingerprint{SourceCommit: "c1", KeyFiles: map[string]string{"a": "1"}}, LastBuildStatus: "QUEUED", LastBuildAt: &at}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"checked_at"`, `"decision"`, `"reasons"`, `"build_id"`, `"build_inputs"`, `"last_build_status"`, `"last_build_at"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("check.json lacks %s: %s", key, data)
		}
	}
	back, err := ParseCheckState(data)
	if err != nil || back.BuildID != "b1" || !back.BuildInputs.Equal(*s.BuildInputs) || !back.LastBuildAt.Equal(at) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if CheckKey("s", "w") != "builds/s/w/check.json" {
		t.Errorf("CheckKey = %s", CheckKey("s", "w"))
	}
}
