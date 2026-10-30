package projectpolicy

import "testing"

// decodeBitbucketNode decodes a policy node built from the parts under test:
// the bitbucket_token block and the profiles block, on top of what Decode
// requires anyway (a version, a budget and the project's own allow_repos). It
// is decodeProfiles' twin for the tests that need the block beside the
// profiles, so a fixture writes only the members its rule is about.
func decodeBitbucketNode(t *testing.T, parts ...string) (*Policy, error) {
	t.Helper()
	raw := `{"version":` + itoa(Version) + `,"budget":{"mode":"enforce"},"allow_repos":["github/acme/app","bitbucket/workspace-a/repo-a"]`
	for _, part := range parts {
		raw += "," + part
	}
	raw += "}"
	p, found, err := Decode([]byte(raw))
	if err != nil {
		return nil, err
	}
	if !found {
		t.Fatal("Decode found no policy")
	}
	return p, nil
}
