package bbtokens

import (
	"context"
	"testing"
)

func TestProbeQualifiedCoverage(t *testing.T) {
	recs := map[string]Record{
		"repo": {Name: "bitbucket-token--ci", Type: TypeRepo,
			Coverage: []string{"bitbucket/acme/fugaro"}, Expires: aDayComesLater},
		"workspace": {Name: "bitbucket-token--ws", Type: TypeWorkspace,
			Coverage: []string{"bitbucket/acme/*"}, Expires: aDayComesLater},
		"project": {Name: "bitbucket-token--proj", Type: TypeProject,
			Coverage: []string{"bitbucket/acme/*"}, ProjectUUID: "{proj-1}", Expires: aDayComesLater},
	}
	for name, rec := range recs {
		if err := rec.Validate(); err != nil {
			t.Errorf("%s: a Validate-clean record was rejected: %v", name, err)
			continue
		}
		f := newFakeBitbucket(t, repoAnswers())
		res := f.verifier().Verify(context.Background(), testToken, rec)
		t.Logf("%s: OK=%v msg=%q calls=%d", name, res.OK, res.Message, len(f.requests()))
	}
	t.Logf("two-segment: %v", repoRecord().Validate())
}
