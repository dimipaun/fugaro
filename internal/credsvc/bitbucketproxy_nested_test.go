package credsvc

// The nested traffic of the Bitbucket proxy route (N1, R4): git-lfs's two
// reads, and the refusal a call for a repository the run is not paired to
// earns.
//
// The route carries a run's whole git traffic, and nested fetches are part of
// it: a submodule's clone is an ordinary fetch of another repository, and every
// LFS file a checkout holds is an object the client asks for by name. Before
// this task neither was classified at all - git-lfs's paths are neither of
// git's two services nor a REST route, so every LFS read was refused as a call
// no run makes (U2) - and the refusal a repository outside the run's own
// earned was a class with no repository in it, which a submodule failure does
// not name.
//
// What the route kept is the rule those calls are decided by: Decide, from S2.
// The repository is the whole of the decision and this task adds no second
// gate - a nested read of a repository the run is not paired to is refused
// exactly as a fetch of a ref there is, and the change here is that the
// refusal SAYS which repository the call asked for (the spec's "a submodule in
// a repo the job's token doesn't cover should fail with a clear message naming
// the repo that isn't covered", and the contract's "the sentence refusing a
// call names the repository the call asked for").

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitproxy"
)

// nestedOID is one git-lfs object id: the lower-case hex a batch answer names
// an object by and the client asks for it with (validOID's own shape).
const nestedOID = "9c1f4c3c2b1a0f9e8d7c6b5a4938271605f4e3d2c1b0a9988776655443322110"

// nestedLFSPath is one git-lfs path of a repository, as the client asks for it
// under the remote's own URL.
func nestedLFSPath(repo, rest string) string { return repo + ".git/info/lfs/objects/" + rest }

// nestedLFSDownload is the batch body a client opens a DOWNLOAD with: the
// operation the call is decided on (LFSOperation) beside the objects it names.
// An upload is the same document with "upload" there, which is the call the
// route refuses (TestProxyRefusesAnLFSBatchUpload).
const nestedLFSDownload = `{"operation":"download","transfers":["basic"],"objects":[{"oid":"` + nestedOID + `","size":1}]}`

// TestProxyRefusesAnUncoveredSubmoduleRepo is the refusal a submodule earns
// when its repository is not one the run's profile serves, and the point of it
// is the sentence: the operator is told WHICH repository the checkout asked
// for, because the run's own repository is not the answer - the submodule's
// URL is, and it is the one thing about the call the operator can act on.
//
// The submodule's clone is an ordinary fetch of another repository, so the
// class is other_repository and the gate that closes is Decide's binding - the
// same gate a direct fetch of another repository meets. What this test adds to
// that one is that the repository a nested call names survives into the
// refusal, and that it survives nowhere else: the audit line and the operator's
// own log line still carry the class and the repository the call was made
// UNDER, and never a byte of what the caller asked for (this route's rule for
// every refusal, and the reason the two are built apart).
func TestProxyRefusesAnUncoveredSubmoduleRepo(t *testing.T) {
	s, up, _ := proxySharedSvc(t)
	sibling := bitbucketSlug(t, proxySiblingRepo)
	submodule := "bitbucket/" + proxySiblingRepo

	resp, ans := s.proxySharedCall(t, http.MethodGet,
		proxyPath(proxySiblingRepo+".git/info/refs?service=git-upload-pack"), nil, s.slug)
	e := proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, gitproxy.ReasonOtherRepository)

	if !strings.Contains(e.Message, submodule) {
		t.Errorf("the refusal's sentence = %q, and it does not name the repository the submodule asked for (%s)", e.Message, submodule)
	}
	if n := up.count(); n != 0 {
		t.Fatalf("the upstream saw %d calls, and a refused submodule reaches bitbucket.org never: %+v", n, up.calls())
	}

	line := s.onlyAuditLine(t)
	if line["run"] != s.run || line["repository"] != s.slug {
		t.Errorf("the audit line = %v, want the run %q of %q", line, s.run, s.slug)
	}
	if line["status"] != ErrorRequestRefused {
		t.Errorf("the audit line = %v, want the refusal's own wire type", line)
	}
	// The name is the caller's, and a name the caller chose belongs in the
	// sentence the operator reads and in no line this service writes: the log
	// is a place an attacker would otherwise write to.
	if strings.Contains(s.logs.String(), proxySiblingRepo) {
		t.Errorf("the operator's line carries %q, a repository the caller asked for and this service never echoes: %s", proxySiblingRepo, s.logs.String())
	}
	if !strings.Contains(s.logs.String(), "class="+gitproxy.ReasonOtherRepository) {
		t.Errorf("the operator's line does not name the class %q: %s", gitproxy.ReasonOtherRepository, s.logs.String())
	}
	if sibling != "" && line["repository"] == sibling {
		t.Errorf("the audit line records the repository the submodule ASKED for: %v", line)
	}
}

// TestProxyRefusesAnLFSWrite: git-lfs's upload is refused, and it is the
// classification that refuses it (R4: "an LFS write is refused").
//
// The upload is a PUT to the very path the download GETs, so nothing in the
// path tells them apart and the method is the whole of the difference - which
// is why the shape is read with the method rather than the path alone. The
// refusal is a call_not_allowed: the call is well-formed, and it is simply not
// one a run makes here (a run's writes go through git-receive-pack, where
// Decide reads the update request git sends in front of the pack, and an
// object upload has no update request to read at all).
//
// The batch path is refused a second time and for the same reason: a PUT to
// .../objects/batch is a write to a route whose only call is a read.
func TestProxyRefusesAnLFSWrite(t *testing.T) {
	for _, tc := range []struct{ name, method, path string }{
		{"an object upload", http.MethodPut, proxyPath(nestedLFSPath(testBitbucketRepo, nestedOID))},
		{"an upload to the batch path", http.MethodPut, proxyPath(nestedLFSPath(testBitbucketRepo, "batch"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// One installation per case: a refusal leaves exactly one audit
			// line, and the line is the whole of what a test can read it in.
			s, up, _ := proxySharedSvc(t)

			resp, ans := s.proxySharedCall(t, tc.method, tc.path, []byte(`{"objects":[]}`), s.slug)
			proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, gitproxy.ReasonCallNotAllowed)

			if n := up.count(); n != 0 {
				t.Fatalf("the upstream saw %d calls, and a refused write reaches bitbucket.org never: %+v", n, up.calls())
			}
			line := s.onlyAuditLine(t)
			if line["status"] != ErrorRequestRefused {
				t.Errorf("the audit line = %v, want the refusal's own wire type", line)
			}
			if class := auditField(line, "purpose"); class != "" {
				t.Errorf("the audit line names the purpose %q, and a call refused before it was classified names none", class)
			}
		})
	}
}

// TestProxyForwardsAnLFSReadToTheLFSRoute: the reads this task carries are
// carried to the right place. Classifying git-lfs's paths as fetches is only
// half of it - the route rebuilds the upstream URL from what the
// classification decided on and never from the caller's path (G13), so a
// classification that named the call as a fetch and nothing more would send
// every LFS read to git's upload-pack route, where bitbucket.org answers a
// batch request with a 404 and a clone fails on its first LFS file.
//
// The batch call and the object download are the protocol's two reads and both
// go to the repository's own /info/lfs path, with the bundle's token as git's
// own x-token-auth Basic - the same credential, the same rebuilt host and the
// same audit line every other call of this route has. The batch's own body is
// the one a client sends a download with, and it is forwarded exactly as it
// was read: the operation in it is what the call was decided on.
func TestProxyForwardsAnLFSReadToTheLFSRoute(t *testing.T) {
	for _, tc := range []struct{ name, method, suffix, body, want string }{
		{"the batch call", http.MethodPost, "batch", nestedLFSDownload, "/" + nestedLFSPath(testBitbucketRepo, "batch")},
		{"an object download", http.MethodGet, nestedOID, "", "/" + nestedLFSPath(testBitbucketRepo, nestedOID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, up, token := proxySharedSvc(t)
			up.answerWith(http.StatusOK, `{"objects":[]}`)

			resp, ans := s.proxySharedCall(t, tc.method, proxyPath(nestedLFSPath(testBitbucketRepo, tc.suffix)), []byte(tc.body), s.slug)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the LFS read answered %d, want 200 (%s)", resp.StatusCode, ans)
			}
			if string(ans) != `{"objects":[]}` {
				t.Errorf("the answer = %q, and the upstream's bytes are relayed unchanged", ans)
			}
			got := up.only(t)
			if got.host != "bitbucket.org" || got.scheme != "https" {
				t.Errorf("the LFS read was addressed to %s://%s, want https://bitbucket.org", got.scheme, got.host)
			}
			if got.path != tc.want {
				t.Errorf("the LFS read's upstream path = %q, want %q", got.path, tc.want)
			}
			if string(got.body) != tc.body {
				t.Errorf("the upstream was sent the body %q, want the bytes the caller sent (%q)", got.body, tc.body)
			}
			wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(bitbucket.GitUsername+":"+token))
			if got.auth != wantAuth {
				t.Errorf("the upstream was shown %q, want the bundle's token as git's own x-token-auth Basic", got.auth)
			}
			line := s.onlyAuditLine(t)
			if line["purpose"] != PurposeGit || line["credential"] != config.BitbucketSecret || line["status"] != StatusOK {
				t.Errorf("the audit line = %v, want the git purpose, the bundle's credential and ok", line)
			}
			s.noTokenAnywhere(t, token, string(ans))
		})
	}
}

// TestProxyRefusesAnLFSBatchUpload: git-lfs's upload is refused where it
// actually arrives, and it arrives as the batch call and not as a PUT.
//
// A client that uploads objects opens with the same POST a download does and
// names "upload" in the body's operation; the PUT to the object path that
// follows is the transfer itself, which a client only makes once the server
// has answered with upload actions. So a route that classified the path and
// forwarded the body unread would hand the caller exactly those actions - the
// run getting a way to write objects into a repository whose refs alone it may
// move - while the PUT that carries the bytes was refused and never reached.
// The refusal is call_not_allowed for every identity, a writing one included,
// and it is taken before a credential is chosen.
//
// A batch that names no operation is refused with it: what the call IS is the
// operation, and a body the rules read no operation out of is a call they did
// not decide about.
func TestProxyRefusesAnLFSBatchUpload(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"an upload", `{"operation":"upload","objects":[{"oid":"` + nestedOID + `","size":1}]}`},
		{"no operation at all", `{"transfers":["basic"],"objects":[{"oid":"` + nestedOID + `","size":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, up, _ := proxySharedSvc(t)

			resp, ans := s.proxySharedCall(t, http.MethodPost, proxyPath(nestedLFSPath(testBitbucketRepo, "batch")), []byte(tc.body), s.slug)
			proxyRefused(t, resp, ans, StatusRequestRefused, ErrorRequestRefused, gitproxy.ReasonCallNotAllowed)

			if n := up.count(); n != 0 {
				t.Fatalf("the upstream saw %d calls, and a refused batch reaches bitbucket.org never: %+v", n, up.calls())
			}
			line := s.onlyAuditLine(t)
			if line["status"] != ErrorRequestRefused {
				t.Errorf("the audit line = %v, want the refusal's own wire type", line)
			}
			if purpose := auditField(line, "purpose"); purpose != PurposeGit {
				t.Errorf("the audit line names the purpose %q, want the git purpose this call was classified as", purpose)
			}
			// The body is the caller's, and an operation the caller chose is
			// caller text like any other: it reaches the refusal's class and
			// no line this service writes.
			if strings.Contains(s.logs.String(), "upload") {
				t.Errorf("the operator's line carries an operation the caller sent: %s", s.logs.String())
			}
		})
	}
}
