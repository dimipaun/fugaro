package credsvc

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// sourcedPolicyDoc is buildPolicyDoc's map beside a catalog whose entry
// "sourced" is a Dockerfile of acme/aurora that no image_map key names, which is
// the shape of the catalog's own `go` entry on an installation whose map holds
// only its other repositories.
func sourcedPolicyDoc() string {
	return strings.Replace(buildPolicyDoc(), `"image_map": {`, `"image_catalog": {"entries": [
		{"name": "sourced", "source": {"kind": "dockerfile", "repo": "`+testRepoPath+`", "path": "Dockerfile"}},
		{"name": "inline", "source": {"kind": "inline", "inline": "FROM scratch\n"}}
	]},
		"image_map": {`, 1)
}

// TestBuildEntryUnmappedIsServedItsSourceRepositoryOnly: an entry the image map
// puts no repository on is served for the one repository its published catalog
// definition is sourced from and for nothing else.
func TestBuildEntryUnmappedIsServedItsSourceRepositoryOnly(t *testing.T) {
	policy := func(s *svc, _ *Config) { s.policy.shared = decodeTestPolicy(t, sourcedPolicyDoc()) }
	call := func(t *testing.T, entry string, repos ...string) int {
		t.Helper()
		s, _ := buildSvc(t, policy)
		resp, _ := s.buildCall(t, buildBody(t, buildRequestBody{
			Entry: entry, BuildID: buildBuildID, Provider: gitprov.KindGitHub,
			Repositories: repos, TTLSeconds: 600, AppID: testAppID,
		}), s.buildToken())
		return resp.StatusCode
	}

	t.Run("its source repository is served", func(t *testing.T) {
		if got := call(t, "sourced", testRepoPath); got != http.StatusOK {
			t.Errorf("an unmapped entry's source repository was answered %d, want %d", got, http.StatusOK)
		}
	})
	t.Run("another repository of the project is refused", func(t *testing.T) {
		if got := call(t, "sourced", testOtherRepoPath); got != StatusRequestRefused {
			t.Errorf("another repository was answered %d, want %d", got, StatusRequestRefused)
		}
	})
	t.Run("the source repository with another is refused", func(t *testing.T) {
		if got := call(t, "sourced", testOtherRepoPath, testRepoPath); got != StatusRequestRefused {
			t.Errorf("the source repository beside another was answered %d, want %d", got, StatusRequestRefused)
		}
	})
	t.Run("an inline entry the map puts nothing on is still refused", func(t *testing.T) {
		if got := call(t, "inline", testRepoPath); got != StatusRequestRefused {
			t.Errorf("an entry with no Dockerfile source was answered %d, want %d", got, StatusRequestRefused)
		}
	})
	t.Run("an entry absent from the catalog is still refused", func(t *testing.T) {
		if got := call(t, "no-such-entry", testRepoPath); got != StatusRequestRefused {
			t.Errorf("an unknown entry was answered %d, want %d", got, StatusRequestRefused)
		}
	})
	t.Run("a mapped entry keeps its map as the authority", func(t *testing.T) {
		if got := call(t, buildEntry, testRepoPath, testOtherRepoPath); got != http.StatusOK {
			t.Errorf("a mapped entry's own repositories were answered %d, want %d", got, http.StatusOK)
		}
		if got := call(t, buildEntry, buildElsewhereRepo); got != StatusRequestRefused {
			t.Errorf("a repository the map puts elsewhere was answered %d, want %d", got, StatusRequestRefused)
		}
	})
}
