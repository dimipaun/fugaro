package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// repo_images_retire_test.go is the legacy repo-to-image member leaving the
// published node (repo_images_retire.go, U1c of docs/image-map-by-provider):
// the step `fugaro update` runs once the credentials service that reads the
// old member has been replaced. Every test is hermetic: the fixture's fake
// RTDB, the rig's own local config, and no cloud.

// mapRetireRig is an installation with an adopted budget database and a policy
// node of its own, driven the way `fugaro update` drives it: a release build,
// no agent marker in the environment, and the deployed rules marker the policy
// stage's own gate asks for.
type mapRetireRig struct {
	r  *initRig
	db *gcpfake.RTDB
}

func newMapRetireRig(t *testing.T) *mapRetireRig {
	t.Helper()
	r, db := stampRig(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	return &mapRetireRig{r: r, db: db}
}

// engine is the run the stage is driven with, at the state `fugaro update`
// reaches this slot in: the credentials service has had its turn (stampWorkdir),
// and what it recorded is this release's image.
func (rg *mapRetireRig) engine(t *testing.T, o *initOptions) *initEngine {
	t.Helper()
	if o.door == "" {
		o.door = updateDoor.name
	}
	e := rigEngine(t, rg.r, o)
	seedInstallationTfvars(t, e, credentialsImageAt(releaseVersion()))
	stampWorkdir(t, e)
	return e
}

// node puts a policy node at the shared path: the legacy member and the new one
// as the caller states them, and nothing else the stage reads.
func (rg *mapRetireRig) node(t *testing.T, node map[string]any) {
	t.Helper()
	rg.db.Set(projectpolicy.PathShared(), node)
}

// withBothMembers is the state U1b leaves on the node: the map in the member
// this release reads and in the one the release behind reads (D3376 as
// amended).
func withBothMembers() map[string]any {
	return map[string]any{
		"version":     projectpolicy.Version,
		"allow_repos": []any{"github/acme/app"},
		"image_map":   map[string]any{"acme/app": "entry-a"},
		// The new member's keys carry the provider in front of the repository's
		// name, escaped as the node stores them (projectpolicy.RepoImages).
		"repo_images": projectpolicy.RepoImages{"github/acme/app": "entry-a"},
	}
}

// nodeRaw reads the published node as the document it is.
func (rg *mapRetireRig) nodeRaw(t *testing.T, e *initEngine) (json.RawMessage, bool) {
	t.Helper()
	_, found, raw, err := readPolicyNode(context.Background(), rigBudgetClient(t, e), e.lc)
	if err != nil {
		t.Fatal(err)
	}
	return raw, found
}

// nodeWrites is how many writes the fake database was sent for the published
// node: the one conditional PUT a removal is, and nothing at all for a run that
// left the node where it was.
func (rg *mapRetireRig) nodeWrites() int {
	n := 0
	for _, rq := range rg.db.Requests() {
		if rq.Method != "GET" && strings.TrimSuffix(rq.Path, ".json") == "/"+projectpolicy.PathShared() {
			n++
		}
	}
	return n
}

// runRetireStage converges the one stage, which is Check, Plan, Apply and Verify
// in the loop's own order.
func runRetireStage(t *testing.T, e *initEngine) (*initflow.Result, string, error) {
	t.Helper()
	out := atTerminal(t, e, "")
	res, err := initflow.Run(t.Context(), []initflow.Stage{newImageMapRetireStage(e)}, e.options())
	return res, out.String(), err
}

// TestRetireImageMapAfterCredentialsService is the stage's whole point: the node
// a release behind can still read loses the legacy member once the service that
// reads it is this release's, in one conditional write, and the generation that
// write landed is recorded in this machine's config so the record does not go
// stale under it.
func TestRetireImageMapAfterCredentialsService(t *testing.T) {
	rg := newMapRetireRig(t)
	e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	rg.node(t, withBothMembers())

	res, out, err := runRetireStage(t, e)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	st := res.Stages[0]
	if st.State != initflow.Changed {
		t.Fatalf("stage = %+v, want the removal as the stage's own outcome:\n%s", st, out)
	}
	if st.Detail != imageMapRetireDone {
		t.Errorf("the line is %q, want %q", st.Detail, imageMapRetireDone)
	}
	raw, found := rg.nodeRaw(t, e)
	if !found {
		t.Fatal("the policy node is gone")
	}
	if _, has, err := jsonMember(raw, imageMapMemberKey); err != nil {
		t.Fatal(err)
	} else if has {
		t.Errorf("the node still carries %s, though this run removed it: %s", imageMapMemberKey, raw)
	}
	node, ok, err := projectpolicy.Decode(raw)
	if err != nil || !ok {
		t.Fatalf("the node does not decode after the write (found=%v): %v", ok, err)
	}
	if node.ImageMap != nil {
		t.Errorf("the node's image_map is %v, want none", node.ImageMap)
	}
	images, fromRepoImages := node.EffectiveRepoImages()
	if !fromRepoImages || images["github/acme/app"] != "entry-a" {
		t.Errorf("the node serves %v (repo_images=%v), want the map the new member carried", images, fromRepoImages)
	}
	if got := rg.nodeWrites(); got != 1 {
		t.Errorf("the node was written %d times, want the one conditional PUT a removal is", got)
	}
	// D854: the write moved the node's generation, and this machine's config
	// names the generation its policy keys were written from — a record left at
	// the generation before this write is what refuses the operator's next edit
	// and offers them a re-seed of their own file.
	etag, _, _, err := readPolicyNode(context.Background(), rigBudgetClient(t, e), e.lc)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(rg.r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lc.PolicyGeneration != etag {
		t.Errorf("the config records generation %q, want the %q this run wrote", lc.PolicyGeneration, etag)
	}
	// The stage is registered after the credentials service's own, which is the
	// whole of why the member leaves at this slot and not with the publish
	// (D3376 as amended).
	pos := stageOrder(t, e)
	if pos[initflow.ImageMapRetire] <= pos[initflow.CredentialsService] {
		t.Errorf("the stage sits at %d and the credentials service at %d: the member may only leave once that service is this release's",
			pos[initflow.ImageMapRetire], pos[initflow.CredentialsService])
	}
}

// stageOrder is every stage the CLI registers, by position.
func stageOrder(t *testing.T, e *initEngine) map[string]int {
	t.Helper()
	pos := map[string]int{}
	for i, s := range initStages(e) {
		pos[s.Name()] = i
	}
	return pos
}

// TestRetireImageMapIdempotent: a node without the legacy member is the state
// the stage exists to reach, and a second run of it writes nothing at all.
func TestRetireImageMapIdempotent(t *testing.T) {
	rg := newMapRetireRig(t)
	e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	rg.node(t, withBothMembers())
	if _, out, err := runRetireStage(t, e); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	before := rg.nodeWrites()

	e2 := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	st, err := newImageMapRetireStage(e2).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Done || st.Detail != imageMapRetireDone {
		t.Errorf("Check = %+v, want done with %q", st, imageMapRetireDone)
	}
	res, out, err := runRetireStage(t, e2)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := res.Stages[0].Detail; got != imageMapRetireDone {
		t.Errorf("the second run's line is %q, want %q", got, imageMapRetireDone)
	}
	if got := rg.nodeWrites(); got != before {
		t.Errorf("the second run wrote the node %d time(s), want none: there is no member to remove", got-before)
	}
}

// TestRetireImageMapWaitsForRepoImages: the legacy member is the map a node
// serves when repo_images is not there (R2), so a node carrying only the old
// one keeps it — taking it away would take the project's map with it. An empty
// repo_images is that same state: EffectiveRepoImages answers the old member
// for it, per member and never per key (F16).
func TestRetireImageMapWaitsForRepoImages(t *testing.T) {
	for _, tc := range []struct {
		name string
		node map[string]any
	}{
		{"the old member alone", map[string]any{
			"version":     projectpolicy.Version,
			"allow_repos": []any{"github/acme/app"},
			"image_map":   map[string]any{"acme/app": "entry-a"},
		}},
		{"an empty repo_images beside it", map[string]any{
			"version":     projectpolicy.Version,
			"allow_repos": []any{"github/acme/app"},
			"image_map":   map[string]any{"acme/app": "entry-a"},
			"repo_images": map[string]any{},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rg := newMapRetireRig(t)
			e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
			rg.node(t, tc.node)
			st, err := newImageMapRetireStage(e).Check(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if st.State != initflow.Skipped || st.Undone {
				t.Fatalf("Check = %+v, want skipped and not owed: the node serves its old member as it does today", st)
			}
			if st.Detail != imageMapRetireWaits {
				t.Errorf("the line is %q, want %q", st.Detail, imageMapRetireWaits)
			}
			res, out, err := runRetireStage(t, e)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := res.Stages[0].State; got != initflow.Skipped {
				t.Errorf("the run's state is %v, want skipped:\n%s", got, out)
			}
			raw, found := rg.nodeRaw(t, e)
			if !found {
				t.Fatal("the policy node is gone")
			}
			if _, has, err := jsonMember(raw, imageMapMemberKey); err != nil {
				t.Fatal(err)
			} else if !has {
				t.Errorf("the node lost %s, and it is the map it serves: %s", imageMapMemberKey, raw)
			}
			if got := rg.nodeWrites(); got != 0 {
				t.Errorf("the node was written %d time(s), want none", got)
			}
		})
	}
}

// TestRetireImageMapIsSkippedInACodingAgentsSession: the session replaces no
// credentials service, so the member the release behind reads stays — and the
// stage is skipped rather than refused, with the sentence the policy stage
// holds the node with (credentialsSessionHeld).
func TestRetireImageMapIsSkippedInACodingAgentsSession(t *testing.T) {
	rg := newMapRetireRig(t)
	e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	rg.node(t, withBothMembers())
	t.Setenv(agentenv.AgentMarkers[0], "1")
	st, err := newImageMapRetireStage(e).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || st.Undone || st.Detail != credentialsSessionHeld {
		t.Fatalf("Check = %+v, want skipped with %q and nothing owed", st, credentialsSessionHeld)
	}
	res, out, err := runRetireStage(t, e)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := res.Stages[0].State; got != initflow.Skipped {
		t.Errorf("the run's state is %v, want skipped:\n%s", got, out)
	}
	if got := rg.nodeWrites(); got != 0 {
		t.Errorf("the node was written %d time(s), want none", got)
	}
}

// TestRetireImageMapLeavesANodeAnotherReleaseWrote: the node every installation
// one release behind carries refuses this release's strict read, and it is the
// state the run's own preview reads it in — before the policy stage, which
// follows it in the order, has migrated the node. The check skips (owed, since
// the member is still on a node that carries it) rather than failing: a failure
// here ends the preview at this stage, which is a run with no review screen at
// all, and the workspace converges read the node with the migration's own
// member and see it. TestUpdateConvergesAWorkspaceFromThePreviousRelease is
// where that is pinned end to end.
func TestRetireImageMapLeavesANodeAnotherReleaseWrote(t *testing.T) {
	rg := newMapRetireRig(t)
	e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	// Version 7, the node a converge's fixtures write for an installation whose
	// release is behind (v7Node): this release's readers refuse a node that far
	// back, because the steps between it and this version are not all shape-free
	// (internal/projectpolicy/migrate.go's shapeFreeSteps).
	rg.node(t, map[string]any{
		"version":     7,
		"allow_repos": []any{"github/acme/app"},
		"image_map":   map[string]any{"acme/app": "entry-a"},
	})
	st, err := newImageMapRetireStage(e).Check(t.Context())
	if err != nil {
		t.Fatalf("the check failed on a node another release wrote, and a preview ends there: %v", err)
	}
	if st.State != initflow.Skipped || !st.Undone || st.Detail != imageMapRetireOtherVersion {
		t.Fatalf("Check = %+v, want skipped and owed with %q", st, imageMapRetireOtherVersion)
	}
	res, out, err := runRetireStage(t, e)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := res.Stages[0].State; got != initflow.Skipped {
		t.Errorf("the run's state is %v, want skipped:\n%s", got, out)
	}
	if got := rg.nodeWrites(); got != 0 {
		t.Errorf("the node was written %d time(s), want none: this release does not read it", got)
	}
}

// TestRetireImageMapHoldsWhileTheServiceIsBehind: a run that did not replace
// the credentials service — a dev build resolves no released image, so the
// stage above leaves it where it is — may not take the member that service
// reads off the node. It holds with the fact it waits for and the door that
// clears it, and leaves the node exactly as it was.
func TestRetireImageMapHoldsWhileTheServiceIsBehind(t *testing.T) {
	rg := newMapRetireRig(t)
	e := rg.engine(t, &initOptions{door: updateDoor.name, yes: true})
	rg.node(t, withBothMembers())
	seedInstallationTfvars(t, e, credentialsImageAt("0.1.0"))
	stampWorkdir(t, e)
	st, err := newImageMapRetireStage(e).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != initflow.Skipped || !st.Undone {
		t.Fatalf("Check = %+v, want skipped and owed", st)
	}
	for _, want := range []string{"credentials service image is not this release's", "holds it there", selfCommand() + " update"} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("the held line does not say %q: %q", want, st.Detail)
		}
	}
	res, out, err := runRetireStage(t, e)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := res.Stages[0].State; got != initflow.Skipped {
		t.Errorf("the run's state is %v, want skipped:\n%s", got, out)
	}
	if got := rg.nodeWrites(); got != 0 {
		t.Errorf("the node was written %d time(s), want none: the service that reads the member is still the release behind's", got)
	}
}
