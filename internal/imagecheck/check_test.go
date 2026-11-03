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
			in.Config = parse(t, strings.Replace(webYAML, "commands:", "image: { apt: [jq] }, commands:", 1))
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
	// A build that ended without failing, yet left the trigger firing
	// (a gate that kept a newer record, say), would do the same again: it
	// backs off too, saying why. An unknown status does not.
	for _, status := range []string{"SUCCESS", "CANCELLED", "EXPIRED", ""} {
		in := baseline(t, now)
		in.BaseDigest = digestB
		fp := Decide(context.Background(), in).Inputs
		in.LastCheck = &CheckState{Decision: Rebuild, BuildID: "b2", BuildInputs: &fp}
		in.LastBuildStatus = status
		d := Decide(context.Background(), in)
		switch {
		case status == "" && d.Decision != Rebuild:
			t.Errorf("after an unknown status: %+v", d)
		case status != "" && (d.Decision != RebuildFailedLast || !slices.Equal(d.Reasons, []string{ReasonBase, ReasonLastBuildIneffective})):
			t.Errorf("after %s: %+v", status, d)
		}
	}

	// A base digest that couldn't be read tonight doesn't make the failed
	// build's inputs look new.
	in := baseline(t, now)
	in.Record.BuiltAt = now.Add(-15 * 24 * time.Hour) // max-age fires
	in.BaseDigest = digestB
	fp := Decide(context.Background(), in).Inputs
	in.LastCheck = &CheckState{Decision: Rebuild, BuildID: "b2", BuildInputs: &fp}
	in.LastBuildStatus = "FAILURE"
	in.BaseDigest = ""
	if d := Decide(context.Background(), in); d.Decision != RebuildFailedLast {
		t.Fatalf("unknown base digest after a failure: %+v", d)
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

// catalogInputs is one catalog entry's state as the daily check reads it
// (PIC4): the copy tonight, the record of the last build of it, and the
// base the entry would be built from.
func catalogInputs(now time.Time) CatalogInputs {
	return CatalogInputs{
		Entry:            "agent",
		DefinitionSHA256: strings.Repeat("a", 64),
		Record: &CatalogRecordState{
			DefinitionSHA256: strings.Repeat("a", 64),
			SourceCommit:     "c1",
			BaseRef:          "base:1", BaseDigest: digestA,
			BuiltAt: now.Add(-2 * time.Hour),
		},
		BaseRef: "base:1", BaseDigest: digestA,
		MaxAge: 24 * time.Hour,
		Now:    now,
	}
}

// TestCheckTriggersOnACatalogDefinitionChange: the daily check's one new
// trigger. A catalog entry whose copied definition is not the one its last
// build read is rebuilt; a copy that did not change is not, and an entry
// with no record is built. The trigger is what makes a rebuild of an entry
// possible at all when neither its definition nor its base has moved (F4),
// since an entry has no commits of its own to watch. How often such a
// rebuild runs is the schedule's business (PIC7), not this decision's.
func TestCheckTriggersOnACatalogDefinitionChange(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)
	cases := []struct {
		name     string
		mod      func(*CatalogInputs)
		decision string
		reasons  []string
	}{
		{"the copy did not change", func(*CatalogInputs) {}, Skip, nil},
		{"the definition changed", func(in *CatalogInputs) {
			in.DefinitionSHA256 = strings.Repeat("b", 64)
		}, Rebuild, []string{ReasonCatalogDefinition}},
		{"no record", func(in *CatalogInputs) { in.Record = nil }, Rebuild, []string{ReasonNoRecord}},
		{"force", func(in *CatalogInputs) { in.Force = true }, Rebuild, []string{ReasonForce}},
		{"force, with nothing changed", func(in *CatalogInputs) {
			in.Force = true
			in.MaxAge = 0
		}, Rebuild, []string{ReasonForce}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := catalogInputs(now)
			tc.mod(&in)
			got := DecideCatalog(in)
			if got.Entry != "agent" || got.Decision != tc.decision || !slices.Equal(got.Reasons, tc.reasons) {
				t.Fatalf("decided %s %v, want %s %v", got.Decision, got.Reasons, tc.decision, tc.reasons)
			}
		})
	}
}

// TestCheckRebuildsACatalogImageOnABaseChange: an entry reuses the base and
// max-age triggers per entry. A base the entry would now be built from is a
// rebuild even when its definition did not move (that is the security
// rebuild: the base moved, so every image built on it is stale), an
// unreadable base digest is a note and never a rebuild, and an image older
// than the entry's max-age is rebuilt.
func TestCheckRebuildsACatalogImageOnABaseChange(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)
	in := catalogInputs(now)
	in.BaseDigest = digestB
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonBase}) {
		t.Fatalf("a moved base: %s %v", got.Decision, got.Reasons)
	}
	in = catalogInputs(now)
	in.BaseRef = "base:2"
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonBase}) {
		t.Fatalf("a changed base ref: %s %v", got.Decision, got.Reasons)
	}
	in = catalogInputs(now)
	in.BaseDigest = ""
	if got := DecideCatalog(in); got.Decision != Skip || !slices.Equal(got.Reasons, []string{ReasonBaseUnknown}) {
		t.Fatalf("an unknown base digest: %s %v", got.Decision, got.Reasons)
	}
	in = catalogInputs(now)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonMaxAge}) {
		t.Fatalf("an image past its max-age: %s %v", got.Decision, got.Reasons)
	}
	// Two triggers at once are recorded in the order they are evaluated.
	in = catalogInputs(now)
	in.DefinitionSHA256 = strings.Repeat("b", 64)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonCatalogDefinition, ReasonMaxAge}) {
		t.Fatalf("a changed definition and an old image: %s %v", got.Decision, got.Reasons)
	}
	// The definition is evaluated before the base, so a night when both moved
	// records both in that order: the base switch runs after the definition
	// comparison and appends to what it recorded, never ahead of it.
	in = catalogInputs(now)
	in.DefinitionSHA256 = strings.Repeat("b", 64)
	in.BaseDigest = digestB
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonCatalogDefinition, ReasonBase}) {
		t.Fatalf("a changed definition and a moved base: %s %v", got.Decision, got.Reasons)
	}
}

// TestCheckRebuildsACatalogImageWhenABakedRepositoriesLockfilesMoved: the
// fourth trigger an entry reuses (reconciliation §4). A baked entry's
// dependency files are what the workflow check's lockfiles trigger watches,
// and an entry has no checkout of its own: the caller reads them from the
// baked repository's tree and says so here, so a moved lockfile is a rebuild
// and a night when nothing moved is not.
func TestCheckRebuildsACatalogImageWhenABakedRepositoriesLockfilesMoved(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)
	in := catalogInputs(now)
	in.Lockfiles = true
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonLockfiles}) {
		t.Fatalf("moved lockfiles: %s %v", got.Decision, got.Reasons)
	}
	in = catalogInputs(now)
	in.Lockfiles = true
	in.DefinitionSHA256 = strings.Repeat("b", 64)
	if got := DecideCatalog(in); got.Decision != Rebuild || !slices.Equal(got.Reasons, []string{ReasonCatalogDefinition, ReasonLockfiles}) {
		t.Fatalf("a changed definition and moved lockfiles: %s %v", got.Decision, got.Reasons)
	}
}

// catalogLastCheck records on in the last check the scheduled run wrote for
// these inputs: the build it submitted, what that build ran with, and — as
// the caller reads it from Cloud Build — the status of that build.
func catalogLastCheck(in *CatalogInputs, status string) {
	inputs := *in
	inputs.Record = nil
	fp := DecideCatalog(inputs).Inputs
	in.LastCheck = &CatalogCheckState{
		Version: CatalogCheckVersion, Decision: Rebuild,
		BuildID: "b1", BuildInputs: &fp, LastBuildStatus: status,
	}
	in.LastBuildStatus = status
}

// TestCheckCatalogEntryIsBackedOffAfterAFailedBuild: the back-off the
// scheduled catalog check reuses from the daily check (D1148). An entry whose
// last build of these very inputs failed is rebuild-failed-last, not a
// rebuild: without this, an entry whose Dockerfile cannot build would be paid
// for on every scheduled run, because a failed build writes no record and the
// max-age trigger keeps firing. A build that is still running leaves it a
// skip, and a build of *other* inputs is no back-off at all.
func TestCheckCatalogEntryIsBackedOffAfterAFailedBuild(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)

	// A failed build of tonight's inputs, with no record since: the entry is
	// paused rather than rebuilt.
	in := catalogInputs(now)
	in.Record = nil
	catalogLastCheck(&in, "FAILURE")
	if got := DecideCatalog(in); got.Decision != RebuildFailedLast || !slices.Equal(got.Reasons, []string{ReasonNoRecord}) {
		t.Fatalf("a failed last build: %s %v, want %s", got.Decision, got.Reasons, RebuildFailedLast)
	}

	// The same, on an entry that has a record: the max-age trigger fires, and
	// the back-off holds it back.
	in = catalogInputs(now)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	catalogLastCheck(&in, "TIMEOUT")
	if got := DecideCatalog(in); got.Decision != RebuildFailedLast || !slices.Equal(got.Reasons, []string{ReasonMaxAge}) {
		t.Fatalf("a failed last build past max-age: %s %v", got.Decision, got.Reasons)
	}

	// Still running: a skip, so two runs never build the same entry at once.
	in = catalogInputs(now)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	catalogLastCheck(&in, "WORKING")
	if got := DecideCatalog(in); got.Decision != Skip || !slices.Equal(got.Reasons, []string{ReasonMaxAge, ReasonBuildRunning}) {
		t.Fatalf("a running last build: %s %v", got.Decision, got.Reasons)
	}

	// A final build that did not fail and did not clear the trigger: the
	// entry is backed off too, and the note says why.
	in = catalogInputs(now)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	catalogLastCheck(&in, "SUCCESS")
	if got := DecideCatalog(in); got.Decision != RebuildFailedLast || !slices.Equal(got.Reasons, []string{ReasonMaxAge, ReasonLastBuildIneffective}) {
		t.Fatalf("an ineffective last build: %s %v", got.Decision, got.Reasons)
	}

	// The inputs moved: the back-off no longer applies, and the entry is
	// rebuilt.
	in = catalogInputs(now)
	in.Record.BuiltAt = now.Add(-25 * time.Hour)
	catalogLastCheck(&in, "FAILURE")
	in.BaseDigest = digestB
	if got := DecideCatalog(in); got.Decision != Rebuild {
		t.Fatalf("a failed build of other inputs: %s %v", got.Decision, got.Reasons)
	}

	// A forced run ignores the back-off, as the workflow check's does.
	in = catalogInputs(now)
	in.Record = nil
	in.Force = true
	catalogLastCheck(&in, "FAILURE")
	if got := DecideCatalog(in); got.Decision != Rebuild {
		t.Fatalf("a forced run: %s %v", got.Decision, got.Reasons)
	}
}

// TestCheckCatalogDecidesEveryEntryOfTheCatalog: the scheduled check's one
// pass (CheckCatalog, D1148). It decides every entry the catalog holds, in
// the catalog's own order, and an entry that is a prebuilt image has nothing
// to build and is passed over — so a project with one catalog gets one
// scheduled check, not one scheduler per entry.
func TestCheckCatalogDecidesEveryEntryOfTheCatalog(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)
	cat := &config.Catalog{Entries: []config.Entry{
		{Name: "agent", Source: config.Source{Kind: config.SourceDockerfile, Repo: "o/r", Path: "Dockerfile"}},
		{Name: "tools", Source: config.Source{Kind: config.SourceDockerfile, Repo: "o/r", Path: "tools/Dockerfile"}},
		{Name: "pinned", Source: config.Source{Kind: config.SourceImage, Image: "ghcr.io/o/image:1"}},
	}}
	unchanged := catalogInputs(now)
	unchanged.Entry = "agent"
	moved := catalogInputs(now)
	moved.Entry, moved.BaseDigest = "tools", digestB
	got, err := CheckCatalog(cat, map[string]CatalogInputs{"agent": unchanged, "tools": moved})
	if err != nil {
		t.Fatalf("CheckCatalog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decided %d entries, want 2 (the prebuilt image is passed over): %+v", len(got), got)
	}
	if got[0].Entry != "agent" || got[0].Decision != Skip {
		t.Errorf("agent: %s %v, want %s", got[0].Decision, got[0].Reasons, Skip)
	}
	if got[1].Entry != "tools" || got[1].Decision != Rebuild || !slices.Equal(got[1].Reasons, []string{ReasonBase}) {
		t.Errorf("tools: %s %v, want %s (%s)", got[1].Decision, got[1].Reasons, Rebuild, ReasonBase)
	}
	// An entry with no inputs read for it is a failure to decide, not a
	// silent skip: the check never guesses what an entry's definition is.
	if _, err := CheckCatalog(cat, map[string]CatalogInputs{"agent": unchanged}); err == nil {
		t.Fatal("CheckCatalog decided an entry it had no inputs for")
	}
}

// TestCatalogCheckStateRoundTrips: the entry's check.json is what the next
// run's back-off reads, so it round-trips through the same bytes the bucket
// holds, and a document with no version is refused rather than read as one.
func TestCatalogCheckStateRoundTrips(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 30, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	s := &CatalogCheckState{
		Version: CatalogCheckVersion, CheckedAt: now, Decision: RebuildFailedLast,
		Reasons: []string{ReasonBase}, BuildID: "b1", LastBuildStatus: "FAILURE", LastBuildAt: &at,
		BuildInputs: &CatalogInputsFingerprint{DefinitionSHA256: strings.Repeat("a", 64), BaseRef: "base:1", BaseDigest: digestA},
	}
	data, err := s.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := ParseCatalogCheckState(data)
	if err != nil || back.BuildID != "b1" || back.LastBuildStatus != "FAILURE" || back.BuildInputs == nil || !back.BuildInputs.Equal(*s.BuildInputs) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if _, err := ParseCatalogCheckState([]byte(`{"decision":"skip"}`)); err == nil {
		t.Fatal("a check state with no version parsed")
	}
	if got, want := CatalogCheckKey("s", "agent"), "builds/s/catalog/agent/check.json"; got != want {
		t.Errorf("CatalogCheckKey = %q, want %q", got, want)
	}
}
