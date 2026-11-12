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
// publishes and this release's own (migrateV4ToV5 moves nothing else), so a
// node the database holds one step back is this document with the older number
// in it.
func atVersion(doc string, v int) json.RawMessage {
	return json.RawMessage(strings.Replace(doc, `"version": 5`, `"version": `+strconv.Itoa(v), 1))
}

// TestRTDBPolicyServesAShapeFreePredecessor: this service is the reader that
// `fugaro update` re-pins before it migrates the policy node (the installation
// stage runs ahead of the policy stage, which is behind the image builds), so a
// node one shape-free step behind this binary must be *served* rather than
// refused: a refusal here is every cascade and Bitbucket-proxied model call of
// the window failing, for a policy whose members are this release's (D990,
// projectpolicy's ReadsVersion). A node the chain cannot read that way is
// refused exactly as it was, and the service's own handler turns that error
// into the call's refusal (server.go's servePolicy).
func TestRTDBPolicyServesAShapeFreePredecessor(t *testing.T) {
	twin := decodeTestPolicy(t, testPolicyDoc())
	for _, tc := range []struct {
		name    string
		version int
		refused bool
	}{
		// The versions are read off the chain and not spelled out: the row
		// below this one has to be a SHAPE-FREE step (migrate.go's
		// shapeFreeSteps), and a literal that the bump of Version left behind
		// names a node the reader refuses and carries instead — served at this
		// release's number, which is what the assertion here used to fail on.
		{"this release's node", projectpolicy.Version, false},
		{"the node one shape-free step behind", projectpolicy.Version - 1, false},
		{"a node the chain cannot read across", 3, true},
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
					t.Fatalf("Shared of a version-%d node = (%v, %v), want a VersionError found %d: a node no reader of this release reads is a refusal and not a policy", tc.version, p, err, tc.version)
				}
				return
			}
			if err != nil || p == nil {
				t.Fatalf("Shared of a version-%d node = (%v, %v), want the policy and no error: the update re-pins this service before it migrates the node", tc.version, p, err)
			}
			if p.Version != tc.version {
				t.Errorf("the policy reads version %d, want %d", p.Version, tc.version)
			}
			if !slices.Equal(p.AllowRepos, twin.AllowRepos) {
				t.Errorf("the node behind reads allow_repos %v, want this release's %v: its members are this release's, and only the number differs", p.AllowRepos, twin.AllowRepos)
			}
		})
	}
}
