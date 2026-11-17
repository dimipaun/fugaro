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

// TestRTDBPolicyServesThisReleasesNodeAndCarriesTheOneBehindIt: this service is
// the reader that `fugaro update` re-pins before it migrates the policy node
// (the installation stage runs ahead of the policy stage, which is behind the
// image builds), so a node one version behind this binary must be *served*
// rather than refused: a refusal here is every cascade and Bitbucket-proxied
// model call of the window failing, which the update may never do to a run in
// flight (CLAUDE.md's HARD RULE).
//
// It is served by a CARRY and not by a tolerance, and the crossing is what made
// the difference (MC21): the step from 8 rewrites craft_pools and crafts, so it
// is not shape-free and no version below this one is read as it stands
// (projectpolicy's ReadsVersion). The carry applies that step to the bytes in
// memory, for this reader alone, and writes nothing back — so the node behind
// is served with its crossing pools split, which is exactly the document the
// operator's own migration writes a moment later. A node below the carry's
// floor is refused, and the handler turns that error into the call's refusal
// (server.go's servePolicy) rather than reading a policy member for member.
func TestRTDBPolicyServesThisReleasesNodeAndCarriesTheOneBehindIt(t *testing.T) {
	twin := decodeTestPolicy(t, testPolicyDoc())
	if projectpolicy.ReadsVersion(projectpolicy.Version - 1) {
		t.Fatal("the version below this one is a shape-free step behind again: this test's rows assume it is not, and a row that passes through the tolerance proves nothing about the carry")
	}
	for _, tc := range []struct {
		name    string
		version int
		want    int
		refused bool
	}{
		{"this release's node", projectpolicy.Version, projectpolicy.Version, false},
		{"the node one version behind, carried forward in memory", projectpolicy.Version - 1, projectpolicy.Version, false},
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
				t.Errorf("the policy reads version %d, want %d: the carry writes this release's version, as the operator's migration does", p.Version, tc.want)
			}
			if !slices.Equal(p.AllowRepos, twin.AllowRepos) {
				t.Errorf("the node behind reads allow_repos %v, want this release's %v: its members are this release's, and only the number differs", p.AllowRepos, twin.AllowRepos)
			}
		})
	}
}
