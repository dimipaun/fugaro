package credsvc

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// policyGetter is RTDBPolicy's one read: the raw node the database holds at
// the path, and the path it was asked for.
type policyGetter struct {
	nodes map[string]json.RawMessage
	path  string
}

func (g *policyGetter) Get(_ context.Context, path string, out any) (bool, error) {
	g.path = path
	raw, ok := g.nodes[path]
	if !ok {
		return false, nil
	}
	*out.(*json.RawMessage) = raw
	return true, nil
}

// atVersion is one of the fixture's documents at another policy version: the
// version member is the whole difference between a node the release behind
// publishes and this release's own, so a node the database holds one step back
// is this document with the older number in it.
func atVersion(doc string, v int) json.RawMessage {
	return json.RawMessage(strings.Replace(doc, `"version": 9`, `"version": `+strconv.Itoa(v), 1))
}

// TestRTDBPolicyServesThisReleasesNodeAndTheOneBehindIt: this service is
// the reader that `fugaro update` re-pins before it migrates the policy node
// (the installation stage runs ahead of the policy stage, which is behind the
// image builds), so a node one version behind this binary must be *served*
// rather than refused: a refusal here is every cascade and Bitbucket-proxied
// model call of the window failing, which the update may never do to a run in
// flight (CLAUDE.md's HARD RULE).
//
// It is served by a CARRY or by the tolerance a shape-free step buys, and which
// of the two is the version's own answer (projectpolicy.ReadsVersion): a step
// that moves the number and nothing else leaves a node this reader reads as it
// stands — version 9 is one, since the `reports_commit` member version 10
// models is a top-level key no version-9 release wrote (R14, D2752) — while a
// step that rewrites the node is applied to the bytes in memory, for this
// reader alone, and nothing is written back. The step from 8 is the second kind
// and is the crossing that made the difference (MC21): it splits craft_pools
// and crafts, so the node behind is served with its crossing pools split,
// exactly the document the operator's own migration writes a moment later.
//
// Both answers are one guarantee — the service serves the node rather than
// refusing it — and both are asserted below, the carry by its own row at the
// newest version the reader does not read as it stands. A node below the
// carry's floor is refused, and the handler turns that error into the call's
// refusal (server.go's servePolicy) rather than reading a policy member for
// member.
func TestRTDBPolicyServesThisReleasesNodeAndTheOneBehindIt(t *testing.T) {
	twin := decodeTestPolicy(t, testPolicyDoc())
	// The version one behind is served either as it stands or by the carry, and
	// which one the row expects is read from the release rather than typed: a
	// later bump that made this step shape-free would otherwise fail here for
	// the statement it had just made true.
	behind := projectpolicy.Version - 1
	behindWant := behind
	if !projectpolicy.ReadsVersion(behind) {
		behindWant = projectpolicy.Version
	}
	// carry is the newest version below this release that no tolerance reaches:
	// a shape-free predecessor is read as it stands, so the carry's own row has
	// to name the newest one a step of a kind it cannot read still separates.
	carry := behind
	for projectpolicy.ReadsVersion(carry) {
		carry--
	}
	if !projectpolicy.Migratable(carry) {
		t.Fatalf("version %d is no longer one the chain carries forward: this test's carry row needs the newest version that is", carry)
	}
	for _, tc := range []struct {
		name    string
		version int
		want    int
		refused bool
	}{
		{"this release's node", projectpolicy.Version, projectpolicy.Version, false},
		{"the node one version behind, served", behind, behindWant, false},
		{"the newest node below this release that only a carry reaches", carry, projectpolicy.Version, false},
		{"a node below the carry's floor", projectpolicy.ShapeFreeFloor - 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &policyGetter{nodes: map[string]json.RawMessage{projectpolicy.PathShared(): atVersion(testPolicyDoc(), tc.version)}}
			p, found, err := RTDBPolicy{DB: g}.Shared(context.Background())
			if g.path != projectpolicy.PathShared() {
				t.Fatalf("Shared read %q, want %q", g.path, projectpolicy.PathShared())
			}
			if !found {
				t.Fatal("found = false, want the node the database holds")
			}
			if tc.refused {
				var ve *projectpolicy.VersionError
				if p != nil || !errors.As(err, &ve) || ve.Found != tc.version {
					t.Fatalf("Shared of a version-%d node = (%v, %v), want a VersionError found %d: a node below the carry's floor is a refusal and not a policy", tc.version, p, err, tc.version)
				}
				return
			}
			if err != nil || p == nil {
				t.Fatalf("Shared of a version-%d node = (%v, %v), want the policy and no error: the update re-pins this service before it migrates the node", tc.version, p, err)
			}
			if p.Version != tc.want {
				t.Errorf("the policy reads version %d, want %d: the reader serves the node it was handed, as it stands where the step is shape-free and carried forward where it is not", p.Version, tc.want)
			}
			if !slices.Equal(p.AllowRepos, twin.AllowRepos) {
				t.Errorf("the node behind reads allow_repos %v, want this release's %v: its members are this release's, and only the number differs", p.AllowRepos, twin.AllowRepos)
			}
		})
	}
}
