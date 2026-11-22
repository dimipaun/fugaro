package orchestrator_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The naming contract of the orchestrator subtree (C-ORCH): the id rule of a
// named orchestrator (D3207, D3218, F36), the graph scope grammar it owns (F40),
// the driver node beside the state and the lease (F18, D3218), the
// `<orchestrator>/<item>` reference a dependency may carry (D3208), the
// project-wide claim path (D3209), and the `orchestrator map --json` document
// (D3221). Each is a rule a reader outside this package relies on: the CLI's
// `orchestrator ls`, the graph command's scope, the console's bridge and the
// ee builder all read these names, so they are pinned here rather than left to
// the callers that happen to agree today.

// longProject and longName are the longest halves the id rule admits: the
// project's own cap (config.ProjectNameRE) and the name's (D3207). The id rule
// and the graph scope grammar share one bound, and the longest id the rule
// admits is exactly it.
var (
	longProject = strings.Repeat("p", 40)
	longName    = strings.Repeat("n", 40)
)

// TestNamingRules pins the orchestrator id rule and the driver node.
func TestNamingRules(t *testing.T) {
	t.Run("an id parses into its project and name", func(t *testing.T) {
		cases := []struct{ id, project, name string }{
			{"fugaro", "fugaro", ""},
			{"a", "a", ""},
			{"my-project", "my-project", ""},
			{longProject, longProject, ""},
			{"fugaro~night", "fugaro", "night"},
			{"a~b", "a", "b"},
			{"fugaro~night-shift-2", "fugaro", "night-shift-2"},
			{"fugaro~2fast", "fugaro", "2fast"},
			{longProject + "~" + longName, longProject, longName},
		}
		for _, c := range cases {
			project, name, ok := orchestrator.ParseOrchestratorID(c.id)
			if !ok || project != c.project || name != c.name {
				t.Errorf("ParseOrchestratorID(%q) = (%q, %q, %v), want (%q, %q, true)",
					c.id, project, name, ok, c.project, c.name)
			}
			if !orchestrator.ValidOrchestratorID(c.id) {
				t.Errorf("%q is an orchestrator id, and the rule refuses it", c.id)
			}
			if !orchestrator.ValidScopeID(c.id) {
				t.Errorf("%q is an orchestrator id, and a graph scope refuses it (F40)", c.id)
			}
		}
	})

	t.Run("the rule refuses what is not an id", func(t *testing.T) {
		cases := []string{
			"",
			"Fugaro",
			"-fugaro",
			"fugaro-",
			"fugaro_night",
			"fugaro~Night",
			"fugaro~",
			"~night",
			"fugaro~night~x",
			"fugaro~-night",
			"fugaro~night_shift",
			"fugaro~night.",
			"fugaro/night",
			"fugaro night",
			"fugaro~night/x",
			strings.Repeat("p", 41),
			strings.Repeat("p", 41) + "~night",
			"fugaro~" + strings.Repeat("n", 41),
			// The claim key is a child of the subtree and not an orchestrator:
			// it begins with `~`, so no id can take it and a reader that lists
			// the subtree's ids never mistakes the two (D3209).
			orchestrator.ClaimsKey,
		}
		for _, id := range cases {
			if orchestrator.ValidOrchestratorID(id) {
				t.Errorf("ValidOrchestratorID(%q) = true, and the id rule refuses it", id)
			}
			if orchestrator.ValidScopeID(id) {
				t.Errorf("ValidScopeID(%q) = true, and the id rule refuses it", id)
			}
			if _, _, ok := orchestrator.ParseOrchestratorID(id); ok {
				t.Errorf("ParseOrchestratorID(%q) parsed, and the id rule refuses it", id)
			}
		}
	})

	t.Run("the id's project half is the project's own name rule", func(t *testing.T) {
		// The default orchestrator's id is the project's name, so an id must
		// admit exactly the names a project may carry — no more and no less,
		// or a project could name an orchestrator it cannot be.
		for _, p := range []string{"fugaro", "a", longProject, "my-project", "Fugaro", "fugaro_", "fugaro-", strings.Repeat("p", 41)} {
			if got, want := orchestrator.ValidOrchestratorID(p), config.ProjectNameRE.MatchString(p); got != want {
				t.Errorf("ValidOrchestratorID(%q) = %v, and the project name rule says %v", p, got, want)
			}
		}
	})

	t.Run("the bound the id rule and the scope grammar share", func(t *testing.T) {
		longest := longProject + "~" + longName
		if got := len(longest); got != orchestrator.MaxOrchestratorIDLength {
			t.Errorf("the longest id is %d bytes, and MaxOrchestratorIDLength is %d", got, orchestrator.MaxOrchestratorIDLength)
		}
		if !orchestrator.ValidOrchestratorID(longest) {
			t.Errorf("the longest id %q is refused", longest)
		}
		if orchestrator.ValidOrchestratorID(longest + "x") {
			t.Errorf("an id past the bound is admitted: %q", longest+"x")
		}
		if orchestrator.MaxOrchestratorNameLength != len(longName) {
			t.Errorf("MaxOrchestratorNameLength is %d, and the name cap is %d", orchestrator.MaxOrchestratorNameLength, len(longName))
		}
		// An id is a key as written: none of its characters is one the
		// database escapes, so the node a writer names and the node O2's rules
		// grant are the same string.
		for _, id := range []string{"fugaro", "fugaro~night", longest} {
			if got := budget.Key(id); got != id {
				t.Errorf("budget.Key(%q) = %q, and an id must be a key as written", id, got)
			}
		}
	})

	t.Run("the two constructors", func(t *testing.T) {
		id, err := orchestrator.DefaultOrchestratorID("fugaro")
		if err != nil || id != "fugaro" {
			t.Errorf("DefaultOrchestratorID(fugaro) = (%q, %v), want (fugaro, nil)", id, err)
		}
		if _, err := orchestrator.DefaultOrchestratorID("Fugaro"); err == nil {
			t.Error("DefaultOrchestratorID(Fugaro) built an id, and Fugaro is not a project name")
		}
		id, err = orchestrator.NamedOrchestratorID("fugaro", "night")
		if err != nil || id != "fugaro~night" {
			t.Errorf("NamedOrchestratorID(fugaro, night) = (%q, %v), want (fugaro~night, nil)", id, err)
		}
		if !orchestrator.ValidOrchestratorID(id) {
			t.Errorf("the id NamedOrchestratorID built, %q, is refused by the rule", id)
		}
		for _, c := range []struct{ project, name string }{{"Fugaro", "night"}, {"fugaro", "Night"}, {"fugaro", ""}, {"", "night"}} {
			if _, err := orchestrator.NamedOrchestratorID(c.project, c.name); err == nil {
				t.Errorf("NamedOrchestratorID(%q, %q) built an id, and the rule refuses one half", c.project, c.name)
			}
		}
	})

	t.Run("whether a named id is addressable is the path rule's own answer", func(t *testing.T) {
		// NamedIDsAreAddressable is the one fact a caller reads without a project:
		// `fugaro orchestrator install` is one unit per name and carries nothing
		// from a project, so the installer cannot ask PathState about the id its
		// unit's daemon would drive. What it must not be is a second opinion about
		// the rule — it is that rule's own answer about a well-formed named id, and
		// it flips to true the day the rule the paths are built with admits one
		// (CG1a), which is when the installer's guard becomes dead weight.
		id, err := orchestrator.NamedOrchestratorID("probe", "probe")
		if err != nil {
			t.Fatal(err)
		}
		if _, pathErr := orchestrator.PathState(id); orchestrator.NamedIDsAreAddressable() != (pathErr == nil) {
			t.Errorf("NamedIDsAreAddressable() = %v, and this release's paths answer %v for %q", orchestrator.NamedIDsAreAddressable(), pathErr, id)
		}
	})

	t.Run("the driver node", func(t *testing.T) {
		// The path is the state node's sibling. The switch of the id checks
		// this package's paths are built through (checkAgentID, NodeIDs, the
		// lease's holder) to this rule is CG1a's, so what is pinned here is the
		// sibling's own name and that a path refuses what the rule refuses.
		path, err := orchestrator.PathDriver("fugaro")
		if err != nil {
			t.Fatalf("PathDriver: %v", err)
		}
		if want := orchestrator.SubtreeRoot + "/fugaro/" + orchestrator.DriverNode; path != want {
			t.Errorf("the driver node is %q, and it is the sibling %q of the state node", path, want)
		}
		state, err := orchestrator.PathState("fugaro")
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSuffix(path, "/"+orchestrator.DriverNode) != strings.TrimSuffix(state, "/"+orchestrator.StateNode) {
			t.Errorf("the driver node %q and the state node %q are not siblings", path, state)
		}
		if _, err := orchestrator.PathDriver("Fugaro"); err == nil {
			t.Error("PathDriver built a path from an id the rule refuses")
		}

		at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
		raw, err := json.Marshal(map[string]any{"principal": "person@example.com", "at": at.Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		d, err := orchestrator.DecodeDriver(raw)
		if err != nil {
			t.Fatalf("DecodeDriver: %v", err)
		}
		if d.Principal != "person@example.com" || !d.At.Equal(at) {
			t.Errorf("the driver node read as %+v", d)
		}
		// Strict, like the state and the lease beside it: a node this release
		// does not model is refused rather than read as a driver of nobody.
		if _, err := orchestrator.DecodeDriver([]byte(`{"principal":"person@example.com","at":"2026-11-22T09:00:00Z","beat":3}`)); err == nil {
			t.Error("a driver node with an unknown field was read, and the decode is strict")
		}
		if _, err := orchestrator.DecodeDriver([]byte(`{"at":"2026-11-22T09:00:00Z"}`)); err == nil {
			t.Error("a driver node naming no principal was read")
		}
	})
}

// TestParseItemRef pins the `<orchestrator>/<item>` reference a dependency may
// carry (D3208) and the claim path an item is held at (D3209).
func TestParseItemRef(t *testing.T) {
	t.Run("a reference is an id and an item", func(t *testing.T) {
		cases := []struct{ ref, orchestrator, item string }{
			{"fugaro/G1", "fugaro", "G1"},
			{"fugaro~night/G1", "fugaro~night", "G1"},
			{"fugaro~night/wi-2", "fugaro~night", "wi-2"},
			// An item of a plan is the `<plan>/<task>` the state keys it by, so
			// the reference carries the plan's own separator too.
			{"fugaro/docs.plan/T-1", "fugaro", "docs.plan/T-1"},
			{"fugaro~night/a/b", "fugaro~night", "a/b"},
		}
		for _, c := range cases {
			ref, err := orchestrator.ParseItemRef(c.ref)
			if err != nil {
				t.Errorf("ParseItemRef(%q): %v", c.ref, err)
				continue
			}
			if ref.Orchestrator != c.orchestrator || ref.Item != c.item {
				t.Errorf("ParseItemRef(%q) = %+v, want %q/%q", c.ref, ref, c.orchestrator, c.item)
			}
			if ref.String() != c.ref {
				t.Errorf("the reference %+v writes as %q, and it was read from %q", ref, ref.String(), c.ref)
			}
		}
	})

	t.Run("a reference the rule refuses", func(t *testing.T) {
		cases := []string{
			"",
			"fugaro",
			"/G1",
			"fugaro/",
			"fugaro~night/",
			"Fugaro/G1",
			"fugaro~Night/G1",
			"fugaro/G 1",
			"fugaro//G1",
			"fugaro/.hidden",
			"~claims/G1",
		}
		for _, ref := range cases {
			if _, err := orchestrator.ParseItemRef(ref); err == nil {
				t.Errorf("ParseItemRef(%q) parsed, and the rule refuses it", ref)
			} else if !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("ParseItemRef(%q) failed with %v, and a refusal is an ErrInvalid", ref, err)
			}
		}
	})

	t.Run("a claim is one node of the subtree, keyed by the item", func(t *testing.T) {
		// A plan task is claimed under itself: its identity is the plan's folder
		// and the task, so two orchestrators that imported the same plan mean
		// the same task, and the ETag decides which of them launches it (D3209).
		for _, item := range []string{"docs.plan/T-1", "a/b"} {
			ref := orchestrator.ItemRef{Orchestrator: "fugaro~night", Item: item}
			path, err := orchestrator.ClaimPath(ref)
			if err != nil {
				t.Errorf("ClaimPath(%q): %v", ref, err)
				continue
			}
			if want := orchestrator.ClaimsRoot + "/" + budget.Key(item); path != want {
				t.Errorf("ClaimPath(%q) = %q, want %q", ref, path, want)
			}
			if !strings.HasPrefix(path, orchestrator.SubtreeRoot+"/") {
				t.Errorf("ClaimPath(%q) = %q, and a claim lives under %s", ref, path, orchestrator.SubtreeRoot)
			}
		}
		// The key the claims hang from is no orchestrator id, which is what
		// keeps the two apart in one subtree.
		if orchestrator.ValidOrchestratorID(orchestrator.ClaimsKey) {
			t.Errorf("%q is an orchestrator id, and the claims node must not be one", orchestrator.ClaimsKey)
		}
		// A plan task's separator reaches the database as one key, as every key
		// of this tree does.
		path, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: "docs.plan/T-1"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(strings.TrimPrefix(path, orchestrator.ClaimsRoot), "/") != 1 {
			t.Errorf("ClaimPath of a plan task is %q, and it must be one key under the claims node", path)
		}
		// An item is not an orchestrator id: an item's id is case-sensitive and
		// a plan task may be deep, where an orchestrator's name is a lower-case
		// slug of one element.
		if _, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: "Fugaro"}); err != nil {
			t.Errorf("ClaimPath of an upper-case work item id: %v", err)
		}
		if _, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: "a/b/c/d/e"}); err != nil {
			t.Errorf("ClaimPath of a deep plan task: %v", err)
		}
		for _, ref := range []orchestrator.ItemRef{
			{Orchestrator: "fugaro", Item: ""},
			{Orchestrator: "fugaro", Item: "G 1"},
			{Orchestrator: "fugaro", Item: ".hidden"},
			{Orchestrator: "Fugaro", Item: "G1"},
			{Orchestrator: "", Item: "G1"},
			{Orchestrator: "fugaro~Night", Item: "G1"},
		} {
			if _, err := orchestrator.ClaimPath(ref); err == nil {
				t.Errorf("ClaimPath(%q) built a path, and the rule refuses one half", ref)
			}
		}
	})

	t.Run("two orchestrators do not collide on one item key", func(t *testing.T) {
		// The finding this pins: a work item id is minted inside one node — a
		// backlog node mints `<day>-<n>` off its own work list (D26) — so the
		// same id in two orchestrators is two different items, and a claim
		// keyed project-wide would let one block the other. The two paths must
		// differ, and the two orchestrators must still be able to claim their
		// own.
		day := "20261122-01"
		first, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: day})
		if err != nil {
			t.Fatalf("ClaimPath: %v", err)
		}
		second, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro~night", Item: day})
		if err != nil {
			t.Fatalf("ClaimPath: %v", err)
		}
		if first == second {
			t.Errorf("both orchestrators' %s claim %q, and they are two different items", day, first)
		}
		if want := orchestrator.ClaimsRoot + "/fugaro~night/" + budget.Key(day); second != want {
			t.Errorf("the named orchestrator's claim is %q, want %q", second, want)
		}
		// And the other direction: the same plan task in two orchestrators is
		// one item, so it is one node, and the second claim is refused by the
		// ETag rather than landing somewhere else.
		a, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: "docs.plan/T-1"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro~night", Item: "docs.plan/T-1"})
		if err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Errorf("the same plan task claimed as %q and %q, and one task is one claim (D3209)", a, b)
		}
		// Neither shape can take the other's key: a plan task's key is one
		// segment under the claims node — its separator is escaped — and a work
		// item's is two, under the orchestrator that minted it.
		asTask, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: "fugaro", Item: "night/" + day})
		if err != nil {
			t.Fatal(err)
		}
		segments := func(path string) int {
			return strings.Count(strings.TrimPrefix(path, orchestrator.ClaimsRoot+"/"), "/")
		}
		if segments(asTask) != 0 || segments(second) != 1 {
			t.Errorf("a plan task's claim %q and a work item's %q are not told apart by their segments", asTask, second)
		}
		if !strings.Contains(asTask, budget.Key("/")) {
			t.Errorf("a plan task's claim %q carries its separator escaped", asTask)
		}
	})

	t.Run("the reference and the claim agree on what an item is", func(t *testing.T) {
		for _, item := range []string{"G1", "docs.plan/T-1", "a/b", "wi-2"} {
			ref, err := orchestrator.ParseItemRef("fugaro/" + item)
			if err != nil {
				t.Fatalf("ParseItemRef(fugaro/%s): %v", item, err)
			}
			if _, err := orchestrator.ClaimPath(ref); err != nil {
				t.Errorf("an item the reference admits, %q, is refused by ClaimPath: %v", ref.Item, err)
			}
		}
	})
}

// TestMapDocRoundTrip pins the `orchestrator map --json` document (D3221): its
// shape, the JSON its fields write as, the rules a reader may rely on, and the
// stub the command answers with until I-ORCH wires the builder in.
func TestMapDocRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	age := 12.5
	started := now.Add(-2 * time.Minute)
	ok := now.Add(-time.Minute)

	full := orchestrator.MapDoc{
		Version:     orchestrator.MapDocVersion,
		Project:     "fugaro",
		GeneratedAt: now,
		Nodes: []orchestrator.MapNode{
			{ID: "fugaro", Name: "", Driver: "person@example.com", ActiveTasks: 2, RunningJobs: 1, LeaseAgeSeconds: &age},
			{ID: "fugaro~night", Name: "night", Driver: "daemon@example.iam.gserviceaccount.com"},
		},
		Edges: []orchestrator.MapEdge{
			{From: "fugaro~night", To: "fugaro", Kind: orchestrator.MapEdgeDependency},
			{From: "fugaro", To: "fugaro~night", Kind: orchestrator.MapEdgeOverlap, Reason: "both orchestrators are on pull request #3541"},
		},
		Health: orchestrator.MapHealth{Sweep: true, StartedAt: &started, OkAt: &ok, At: &now},
	}

	t.Run("the document survives a round trip", func(t *testing.T) {
		raw, err := orchestrator.EncodeMapDoc(full)
		if err != nil {
			t.Fatalf("EncodeMapDoc: %v", err)
		}
		back, err := orchestrator.DecodeMapDoc(raw)
		if err != nil {
			t.Fatalf("DecodeMapDoc: %v", err)
		}
		if !reflect.DeepEqual(back, full) {
			t.Errorf("the document round-tripped as\n%+v\nand it was\n%+v", back, full)
		}
	})

	t.Run("the fields write as the contract names them", func(t *testing.T) {
		raw, err := orchestrator.EncodeMapDoc(full)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"version", "project", "generated_at", "nodes", "edges", "health"} {
			if _, ok := doc[key]; !ok {
				t.Errorf("the document carries no %q: %s", key, raw)
			}
		}
		var node map[string]json.RawMessage
		if err := json.Unmarshal([]byte(must(raw, "nodes", 0)), &node); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"id", "name", "driver", "active_tasks", "running_jobs", "lease_age_seconds"} {
			if _, ok := node[key]; !ok {
				t.Errorf("a node carries no %q: %s", key, raw)
			}
		}
		var edge map[string]json.RawMessage
		if err := json.Unmarshal([]byte(must(raw, "edges", 0)), &edge); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"from", "to", "kind", "reason"} {
			if _, ok := edge[key]; !ok {
				t.Errorf("an edge carries no %q: %s", key, raw)
			}
		}
		var health map[string]json.RawMessage
		if err := json.Unmarshal(doc["health"], &health); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"sweep", "started_at", "ok_at", "at", "note"} {
			if _, ok := health[key]; !ok {
				t.Errorf("the health carries no %q: %s", key, raw)
			}
		}
		// An age a reader cannot read is null and never a figure: the console's
		// own convention for an unavailable age.
		if got := string(must(raw, "nodes", 1)); !strings.Contains(got, `"lease_age_seconds":null`) {
			t.Errorf("a node with no readable lease stamp writes %s, and an unreadable age is null", got)
		}
	})

	t.Run("the two lists are never null", func(t *testing.T) {
		raw, err := orchestrator.EncodeMapDoc(orchestrator.MapDoc{
			Version: orchestrator.MapDocVersion, Project: "fugaro", GeneratedAt: now,
		})
		if err != nil {
			t.Fatalf("EncodeMapDoc of an empty map: %v", err)
		}
		if !strings.Contains(string(raw), `"nodes":[]`) || !strings.Contains(string(raw), `"edges":[]`) {
			t.Errorf("an empty map writes %s, and its two lists are lists", raw)
		}
		if _, err := orchestrator.DecodeMapDoc(raw); err != nil {
			t.Errorf("an empty map does not read back: %v", err)
		}
	})

	t.Run("the rules a reader relies on", func(t *testing.T) {
		bad := []struct {
			name string
			doc  orchestrator.MapDoc
		}{
			{"a version this release does not write", orchestrator.MapDoc{Version: orchestrator.MapDocVersion + 1, Project: "fugaro"}},
			{"no project", orchestrator.MapDoc{Version: orchestrator.MapDocVersion}},
			{"a project that is not a project name", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "Fugaro"}},
			{"a node of another project", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "belong"}}}},
			{"a node whose name is not its id's", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro~night", Name: "day"}}}},
			{"two nodes with one id", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro"}, {ID: "fugaro"}}}},
			{"a negative count", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro", ActiveTasks: -1}}}},
			{"an edge kind that is neither", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro"}, {ID: "fugaro~night", Name: "night"}},
				Edges: []orchestrator.MapEdge{{From: "fugaro", To: "fugaro~night", Kind: "conflict"}}}},
			{"an edge to an orchestrator the map does not draw", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro"}},
				Edges: []orchestrator.MapEdge{{From: "fugaro", To: "fugaro~night", Kind: orchestrator.MapEdgeOverlap}}}},
			{"an edge from an orchestrator to itself", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Nodes: []orchestrator.MapNode{{ID: "fugaro"}},
				Edges: []orchestrator.MapEdge{{From: "fugaro", To: "fugaro", Kind: orchestrator.MapEdgeDependency}}}},
			{"a heartbeat with the sweep off", orchestrator.MapDoc{Version: orchestrator.MapDocVersion, Project: "fugaro",
				Health: orchestrator.MapHealth{Sweep: false, At: &now}}},
		}
		for _, c := range bad {
			if _, err := orchestrator.EncodeMapDoc(c.doc); err == nil {
				t.Errorf("%s was encoded, and the document's own rules refuse it", c.name)
			}
			raw, err := json.Marshal(c.doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := orchestrator.DecodeMapDoc(raw); err == nil {
				t.Errorf("%s was decoded, and the document's own rules refuse it", c.name)
			}
		}
	})

	t.Run("a document built in Go cannot spell an absent field", func(t *testing.T) {
		// EncodeMapDoc fills the two lists in and DecodeMapDoc reads the raw
		// wire, so neither reaches ValidateMapDoc's own envelope: a document
		// built in Go has no way to spell an absent field, which is what a
		// nil list and a zero time are here.
		valid := orchestrator.MapDoc{
			Version: orchestrator.MapDocVersion, Project: "fugaro", GeneratedAt: now,
			Nodes: []orchestrator.MapNode{}, Edges: []orchestrator.MapEdge{},
		}
		if err := orchestrator.ValidateMapDoc(valid); err != nil {
			t.Fatalf("the document these cases are cut from is refused: %v", err)
		}
		noNodes, noEdges, noTime := valid, valid, valid
		noNodes.Nodes = nil
		noEdges.Edges = nil
		noTime.GeneratedAt = time.Time{}
		for _, c := range []struct {
			name string
			doc  orchestrator.MapDoc
			want string
		}{
			{"the nodes are null", noNodes, "the nodes are null"},
			{"the edges are null", noEdges, "the edges are null"},
			{"no time it was assembled", noTime, "carries no time it was assembled"},
		} {
			t.Run(c.name, func(t *testing.T) {
				err := orchestrator.ValidateMapDoc(c.doc)
				if !errors.Is(err, orchestrator.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
					t.Errorf("a document with %s read as %v, want ErrInvalid saying so", c.name, err)
				}
			})
		}
	})

	t.Run("a document this release does not model", func(t *testing.T) {
		raw, err := orchestrator.EncodeMapDoc(full)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		doc["sweep_at"] = now.Format(time.RFC3339)
		patched, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := orchestrator.DecodeMapDoc(patched); err == nil {
			t.Error("a document with a field this release does not model was read, and the decode is strict")
		}
	})

	t.Run("a field the contract always writes", func(t *testing.T) {
		// Every field the map document always carries, absent and null. The
		// struct decode reads either as a zero value — a nil list, a zero
		// health, a zero time — so a reader that only checked the decoded
		// document would accept a map with no nodes at all as a quiet project.
		raw, err := orchestrator.EncodeMapDoc(full)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := orchestrator.DecodeMapDoc(raw); err != nil {
			t.Fatalf("the document these cases are cut from does not read back: %v", err)
		}
		var complete map[string]json.RawMessage
		if err := json.Unmarshal(raw, &complete); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name string
			key  string
			null bool
		}{
			{"nodes absent", "nodes", false},
			{"nodes null", "nodes", true},
			{"edges absent", "edges", false},
			{"edges null", "edges", true},
			{"health absent", "health", false},
			{"health null", "health", true},
			{"generated_at absent", "generated_at", false},
			{"generated_at null", "generated_at", true},
		} {
			t.Run(c.name, func(t *testing.T) {
				doc := make(map[string]json.RawMessage, len(complete))
				for k, v := range complete {
					doc[k] = v
				}
				spelled := "absent"
				if c.null {
					spelled = "null"
					doc[c.key] = json.RawMessage("null")
				} else {
					delete(doc, c.key)
				}
				patched, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := orchestrator.DecodeMapDoc(patched); !errors.Is(err, orchestrator.ErrInvalid) {
					t.Errorf("a document whose %q is %s read as %v, want ErrInvalid", c.key, spelled, err)
				}
			})
		}
	})

	t.Run("the stub answers with the empty document", func(t *testing.T) {
		d, err := orchestrator.BuildMap("fugaro", now)
		if err != nil {
			t.Fatalf("BuildMap: %v", err)
		}
		if d.Project != "fugaro" || !d.GeneratedAt.Equal(now) || len(d.Nodes) != 0 || len(d.Edges) != 0 {
			t.Errorf("the stub answered %+v, and the empty document is the project and no node", d)
		}
		if d.Health.Sweep || d.Health.Note == "" {
			t.Errorf("the stub's health is %+v, and a map with no builder says the sweep is off and why", d.Health)
		}
		raw, err := orchestrator.EncodeMapDoc(d)
		if err != nil {
			t.Fatalf("the stub's document does not encode: %v", err)
		}
		if _, err := orchestrator.DecodeMapDoc(raw); err != nil {
			t.Fatalf("the stub's document does not read back: %v", err)
		}
	})

	t.Run("the sanitised document keeps every identity", func(t *testing.T) {
		d := full
		d.Nodes = append([]orchestrator.MapNode(nil), full.Nodes...)
		d.Edges = append([]orchestrator.MapEdge(nil), full.Edges...)
		d.Nodes[1].Driver = "daemon\u001b[2J@example.com"
		d.Edges[1].Reason = "one\nline and another"
		d.Health.Note = "a note\twith a tab"
		d.Sanitise()
		if strings.ContainsRune(d.Nodes[1].Driver, 0x1b) || strings.ContainsRune(d.Edges[1].Reason, '\n') || strings.ContainsRune(d.Health.Note, '\t') {
			t.Errorf("the sanitised document still carries what a terminal would obey: %+v", d)
		}
		if d.Nodes[1].ID != "fugaro~night" || d.Edges[1].Kind != orchestrator.MapEdgeOverlap {
			t.Errorf("the sanitised document changed an identity: %+v", d)
		}
		if _, err := orchestrator.EncodeMapDoc(d); err != nil {
			t.Errorf("the sanitised document no longer encodes: %v", err)
		}
	})
}

// must is one field of an encoded document, as it was written.
func must(raw []byte, list string, i int) string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(doc[list], &items); err != nil {
		panic(err)
	}
	return string(items[i])
}
