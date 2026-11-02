package credsvc

// The sign route's tests (C3.9e): the framed message a verdict's signature is
// taken over, the answer's shape, the refusals, the one-signature mark, the
// redaction of the caller's own two tokens, and the environment a child of this
// package's own tests is started from (docs/c39-review-identity/spec.md §5.2,
// §5.6, D1073, D1074).
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the policy is a decoded document,
// the state is a map, and the one-signature marks are the same conditional-write
// fake the probe's lease uses. Nothing in this file is, or looks like, a
// credential (Convention 2): every key and token is built at run time
// (fakeCred, reviewKey, signJWT).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
)

// signSvc is the fixture behind a review caller that may sign: C3.9a's pairing,
// a bundle holding the account's review-signing key, and the conditional write
// the mark is taken with. It is the fixture every test below starts from, and
// each one of them takes it apart one way.
func signSvc(t *testing.T, extra ...func(*svc, *Config)) *svc {
	t.Helper()
	return newReviewSvcSpelled(t, testReviewSA, testReviewSA, append([]func(*svc, *Config){
		func(s *svc, cfg *Config) {
			s.src.docs = [][]byte{reviewSigningDoc(t)}
			cfg.Signs = s.probes
		},
	}, extra...)...)
}

// signKey is the key bytes the fixture's document holds, decoded the way the
// service's accessor decodes them, so what a test recomputes a MAC with is the
// key the service signed under and not a second spelling of it.
func signKey(t *testing.T) []byte {
	t.Helper()
	raw, err := hex.DecodeString(reviewKey(t, "current"))
	if err != nil {
		t.Fatalf("the fixture's own review-signing key is not hex: %v", err)
	}
	return raw
}

// signBody is a sign call's body: the digest the artifact writer computed.
func signBody(digest string) string { return `{"digest":"` + digest + `"}` }

// testDigest is the digest a sign call carries: the SHA-256 of a string this
// test names, so the signed bytes are a value a test can recompute rather than
// a literal that could drift from the frame.
func testDigest(t *testing.T, of string) string {
	t.Helper()
	sum := sha256.Sum256([]byte("fugaro-test-artifact-" + t.Name() + "-" + of))
	return hex.EncodeToString(sum[:])
}

// signCall sends one sign call as the fixture's review run and reads the
// answer.
func (s *svc) signCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.signTokens(t, body, s.reviewRunToken(), s.reviewJobToken(), s.job, mutate...)
}

// signTokens is signCall with the two identities and the reported job name
// given rather than minted, for the calls that must present one the service
// refuses.
func (s *svc) signTokens(t *testing.T, body, runTok, jobTok, job string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	resp := s.postTokens(t, SignPath, body, runTok, jobTok, job, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// signAnswer reads a successful sign answer strictly: the two ends of this
// contract are this repository's code (Convention 6).
func signAnswer(t *testing.T, body []byte) SignResponse {
	t.Helper()
	var ans SignResponse
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the sign answer is not one this contract can read: %v (%s)", err, body)
	}
	return ans
}

// signMark reads the one-signature mark of one run of the fixture's repository.
func (s *svc) signMark(t *testing.T, run string) (reviewSign, bool) {
	t.Helper()
	var m reviewSign
	_, found, err := s.probes.GetETag(context.Background(), reviewSignPath(s.slug, run), &m)
	if err != nil {
		t.Fatalf("reading the one-signature mark: %v", err)
	}
	return m, found
}

// signMarks is every one-signature mark the fixture's database holds, so a test
// can state that a refusal wrote NONE rather than that the one mark it expected
// is missing.
func (s *svc) signMarks(t *testing.T) []string {
	t.Helper()
	s.probes.mu.Lock()
	defer s.probes.mu.Unlock()
	var out []string
	for path := range s.probes.nodes {
		if strings.HasPrefix(path, pathReviewSigns+"/") {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out
}

// TestSignRouteAnswersAnHMACOfTheFramedMessageUnderTheAccountsKey is C3.9e's
// red step: the route answers the HMAC-SHA-256 of the FRAMED message — the
// version tag, the verified repository slug, the verified run and the request's
// digest — under the account's review-signing key. Before this task the path is
// not in ServeHTTP's set at all, so this call is the service's 404 (D143's "no
// admin endpoint" is kept by there being no other route).
//
// The frame is what makes a signature valid for one run of one repository
// (D1073): a bare digest would be a signature any run of any account could
// present, and this test recomputes the MAC from the three parts rather than
// from anything the service hands back.
func TestSignRouteAnswersAnHMACOfTheFramedMessageUnderTheAccountsKey(t *testing.T) {
	s := signSvc(t)
	digest := testDigest(t, "verdict")
	resp, body := s.signCall(t, signBody(digest))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	ans := signAnswer(t, body)
	mac := hmac.New(sha256.New, signKey(t))
	mac.Write([]byte(frameVersion + "\n" + s.slug + "\n" + s.run + "\n" + digest))
	if want := hex.EncodeToString(mac.Sum(nil)); ans.Signature != want {
		t.Errorf("signature = %q, want the HMAC of the framed message %q", ans.Signature, want)
	}

	mark, found := s.signMark(t, s.run)
	if !found {
		t.Fatal("the run has no one-signature mark after its first sign: the mark is taken before the answer")
	}
	if mark.Slug != s.slug || mark.Run != s.run {
		t.Errorf("the mark names %q/%q, want the verified run %q/%q", mark.Slug, mark.Run, s.slug, s.run)
	}
	if mark.At.IsZero() {
		t.Error("the mark carries no time: the node is read by an operator")
	}
	if n := s.src.readCount(); n != 1 {
		t.Errorf("the bundle was read %d times, want once: the key is what the signature is made of", n)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: a signature is not a model call", n)
	}
}

// TestSignAnswerNamesTheAlgorithmAndIsLowercaseHex: the answer names the
// construction it signed with (D892) and renders the MAC lowercase hex, so
// neither end can quietly accept a weaker one. The answer is two fields and no
// more — a field the contract does not have would be a field C3.9f has to
// ignore — and it is no-store.
func TestSignAnswerNamesTheAlgorithmAndIsLowercaseHex(t *testing.T) {
	s := signSvc(t)
	resp, body := s.signCall(t, signBody(testDigest(t, "algorithm")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("the answer is not JSON: %v (%s)", err, body)
	}
	if len(raw) != 2 {
		t.Errorf("the answer carries %d fields, want the algorithm and the signature: %v", len(raw), raw)
	}
	if got := raw["alg"]; got != signAlgorithm {
		t.Errorf("alg = %v, want %q", got, signAlgorithm)
	}
	sig, _ := raw["signature"].(string)
	if sig == "" {
		t.Fatalf("the answer carries no signature: %s", body)
	}
	if sig != strings.ToLower(sig) || !digestRE.MatchString(sig) {
		t.Errorf("signature = %q, want 64 lowercase hex characters", sig)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: a cached verdict signature is one this service did not make for the next caller", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
}

// TestFrameBindsTheVerifiedSlugAndRunAndNotARequestField: the frame is built
// from the identity the service verified, and a body that tries to name either
// half is refused as a field this contract does not have rather than believed
// (D1073). A caller-writable field is the one thing the frame exists to
// exclude, and strict decoding is what makes the attempt a refusal instead of a
// field quietly dropped.
func TestFrameBindsTheVerifiedSlugAndRunAndNotARequestField(t *testing.T) {
	t.Run("a body naming the run or the repository is refused", func(t *testing.T) {
		s := signSvc(t)
		digest := testDigest(t, "named")
		for _, body := range []string{
			`{"digest":"` + digest + `","run":"` + s.run + `"}`,
			`{"digest":"` + digest + `","repository":"` + s.slug + `"}`,
			`{"digest":"` + digest + `","slug":"` + s.slug + `"}`,
			`{"digest":"` + digest + `","fr":"` + s.run + `"}`,
		} {
			resp, answer := s.signCall(t, body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("a body naming a frame field was answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
			}
			if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
			}
		}
		if marks := s.signMarks(t); len(marks) != 0 {
			t.Errorf("a refused call left %v behind, want no mark at all", marks)
		}
	})

	t.Run("two runs of one repository sign different bytes", func(t *testing.T) {
		s := signSvc(t)
		digest := testDigest(t, "two-runs")
		one := signedMAC(t, s, s.reviewRunToken(), digest)
		// The same repository, another run: only the run identity's own fr
		// moves, and the frame's third field moves with it. The other run id is
		// the fixture's own with its four-character suffix replaced, so it is a
		// run id the launcher could have minted.
		other := s.run[:len(s.run)-4] + "cd34"
		two := signedMAC(t, s, s.reviewRunTokenFor(other), digest)
		if one == two {
			t.Errorf("two runs of one repository signed the same bytes: %q", one)
		}
		mac := hmac.New(sha256.New, signKey(t))
		mac.Write([]byte(frameVersion + "\n" + s.slug + "\n" + other + "\n" + digest))
		if want := hex.EncodeToString(mac.Sum(nil)); two != want {
			t.Errorf("the second run's signature = %q, want the frame over its own run %q", two, want)
		}
		if _, found := s.signMark(t, other); !found {
			t.Error("the second run has no mark of its own: the marks are keyed by (slug, run)")
		}
	})
}

// reviewRunTokenFor is a review run identity of the fixture's repository with
// its run id replaced, which is the one field of the frame a test can vary
// without another fixture.
func (s *svc) reviewRunTokenFor(run string) string {
	return s.reviewRunToken(func(c map[string]any) {
		c["fr"] = run
		c["sub"] = "r~" + s.slug + "~" + run
		c["user_id"] = c["sub"]
	})
}

// signedMAC is one call's signature, read from the answer.
func signedMAC(t *testing.T, s *svc, runTok, digest string) string {
	t.Helper()
	resp, body := s.signTokens(t, signBody(digest), runTok, s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	return signAnswer(t, body).Signature
}

// TestSignRouteRefusesADigestThatIsNotThirtyTwoBytesOfLowercaseHex: the digest
// is the one field the request carries, and it is held to its shape before
// anything is read or written — an uppercase, short, long or non-hex digest is
// not the artifact's SHA-256, and a frame built over one would be a signature
// over bytes no artifact writer computed. The refusal names the field and never
// what the call sent.
func TestSignRouteRefusesADigestThatIsNotThirtyTwoBytesOfLowercaseHex(t *testing.T) {
	digest := testDigest(t, "shape")
	for _, tc := range []struct {
		name string
		body string
	}{
		{"uppercase", signBody(strings.ToUpper(digest))},
		{"too short", signBody(digest[:63])},
		{"too long", signBody(digest + "0")},
		{"not hex", signBody(strings.Repeat("z", 64))},
		{"empty", signBody("")},
		{"absent", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := signSvc(t)
			resp, answer := s.signCall(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
			}
			got := refusal(t, resp.StatusCode, answer)
			if got.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
			}
			if !strings.Contains(got.Message, "digest") {
				t.Errorf("the refusal is %q, want it to name the field that failed", got.Message)
			}
			if tc.name != "empty" && tc.name != "absent" && strings.Contains(string(answer), digest) {
				t.Errorf("the refusal repeats the digest the call carried: %s", answer)
			}
			if marks := s.signMarks(t); len(marks) != 0 {
				t.Errorf("a refused digest left %v behind, want no mark at all", marks)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want none: a request the contract refuses reads no credential", n)
			}
		})
	}
}

// TestSignRouteRefusesAnUnknownFieldAndAnOversizeBody: the body decodes
// strictly and is bounded, as every body of this contract is (Convention 6).
// The bound is checked before the bundle is read, so a body that is not a sign
// call reads no credential.
func TestSignRouteRefusesAnUnknownFieldAndAnOversizeBody(t *testing.T) {
	t.Run("an unknown field", func(t *testing.T) {
		s := signSvc(t)
		resp, answer := s.signCall(t, `{"digest":"`+testDigest(t, "unknown")+`","alg":"MD5"}`)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
		}
		if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorRequestRefused {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
		}
	})

	t.Run("an oversize body", func(t *testing.T) {
		s := signSvc(t)
		big := `{"digest":"` + testDigest(t, "oversize") + `","x":"` + strings.Repeat("a", maxMintRequestBytes) + `"}`
		resp, answer := s.signCall(t, big)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
		}
		if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorRequestRefused {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the bound is checked before a credential is read", n)
		}
	})
}

// TestSignRefusalsNameTheCredentialTheAccountWouldSignWith: a refusal names
// what it refused, and for a sign that means the entry the caller's own key
// would be at. The entry is a fact of the VERIFIED identity and not of the
// body, so a call whose body this contract refuses still records a sign an
// operator can account for — the shape a mint's lines already have, where the
// App key's name is known before the mint's own body is looked at.
//
// The exception is the caller whose account is in no row of the review table:
// it has no entry to name, and its line carries no credential — which is also
// why the class refusal below it names none.
func TestSignRefusalsNameTheCredentialTheAccountWouldSignWith(t *testing.T) {
	name := config.ReviewSigningKeySecret(testReviewAccount)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a digest of the wrong shape", signBody("not-a-digest")},
		{"a field this contract does not know", `{"digest":"` + testDigest(t, "named") + `","run":"r"}`},
		{"no digest at all", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := signSvc(t)
			resp, body := s.signCall(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			lines := parseAuditLines(t, s.audit.String())
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
			}
			checkAuditShape(t, lines[0])
			for field, want := range map[string]string{
				"credential": name,
				"purpose":    PurposeReviewSign,
				"tier":       "",
				"status":     ErrorRequestRefused,
				"caller":     testReviewSA,
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("%s = %v, want %q", field, got, want)
				}
			}
		})
	}

	t.Run("an account with no entry to name", func(t *testing.T) {
		s := signSvc(t)
		resp, body := s.signTokens(t, signBody("not-a-digest"), s.runToken(), s.jobToken(), s.job)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		lines := parseAuditLines(t, s.audit.String())
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		if got := lines[0]["credential"]; got != "" {
			t.Errorf("credential = %v, want no credential: this account is in no row of the review table", got)
		}
	})
}

// TestSignRouteRefusesARunThatHasEnded: signing is an end-of-run act (F11).
// checkLive already refuses a run whose write-once outcome is written, and this
// route does not weaken it: the outcome is planted as the runner writes it, and
// the call is refused with no signature and no mark — a signature obtained
// after the run ended would be a signature for bytes a later process chose.
//
// The refusal is answered before the identity is verified, so it writes no
// audit line: the area rule counts the refusals of a caller the service has an
// identity for, and this one has none.
func TestSignRouteRefusesARunThatHasEnded(t *testing.T) {
	s := signSvc(t)
	s.db.set(budget.PathOutcome(budget.Day(s.now), s.slug, s.run))
	resp, body := s.signCall(t, signBody(testDigest(t, "ended")))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if strings.Contains(string(body), `"signature"`) {
		t.Errorf("the refusal carries a signature: %s", body)
	}
	if marks := s.signMarks(t); len(marks) != 0 {
		t.Errorf("an ended run's sign attempt left %v behind, want no mark at all", marks)
	}
	if n := s.audit.Len(); n != 0 {
		t.Errorf("the refusal wrote %d bytes of audit line, want none: the identity was never verified", n)
	}
}

// TestSignRouteRefusesAReviewAccountWithNoKeyYet: the key is read BEFORE the
// mark, and this is the half that holds the order in place (§5.2, §6.4). A run
// whose account has no key yet is refused and NOTHING is written, so the
// operator's `creds set` and a re-run sign the head — where a mark taken and
// then found unanswerable would be an unsigned review and a re-review.
//
// The refusal is typed and is never a signature under something else: the
// document holds the account's model credential and no review-signing entry,
// which is a run whose key the deployment has not set yet.
func TestSignRouteRefusesAReviewAccountWithNoKeyYet(t *testing.T) {
	s := signSvc(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{testBundleDoc(t)}
	})
	resp, body := s.signCall(t, signBody(testDigest(t, "no-key")))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
	}
	if strings.Contains(string(body), `"signature"`) {
		t.Errorf("the refusal carries a signature: %s", body)
	}
	if marks := s.signMarks(t); len(marks) != 0 {
		t.Errorf("a run with no key yet left %v behind, want no mark at all: the key is read before the mark", marks)
	}
	lines := parseAuditLines(t, s.audit.String())
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
	}
	if got := lines[0]["credential"]; got != config.ReviewSigningKeySecret(testReviewAccount) {
		t.Errorf("the refusal's line names credential %v, want the entry it would have read %q", got, config.ReviewSigningKeySecret(testReviewAccount))
	}
}

// TestSignRouteRefusesACodingCallerAndAnUnadmittedClass: the route is one only
// a review job's account may call, because the key it reads is the KEY OF THE
// CALLER'S ACCOUNT — a per-repository job has no review-signing entry, and a
// route that looked one up for it would be naming a credential the deployment
// never configured.
//
// The coding caller here is otherwise perfectly good — its two identities are
// whole and it is bound to the run identity's own repository — so the class is
// the only thing that can refuse it. The shared project job's account is
// refused whole, as it is on every other route (Global Constraint 6).
func TestSignRouteRefusesACodingCallerAndAnUnadmittedClass(t *testing.T) {
	t.Run("a coding caller", func(t *testing.T) {
		s := signSvc(t)
		resp, body := s.signTokens(t, signBody(testDigest(t, "coding")), s.runToken(), s.jobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
		if !strings.Contains(got.Message, "review") {
			t.Errorf("the refusal is %q, want it to name the class the route is served to", got.Message)
		}
		if marks := s.signMarks(t); len(marks) != 0 {
			t.Errorf("a refused caller left %v behind, want no mark at all", marks)
		}
	})

	t.Run("a shared project job's account", func(t *testing.T) {
		s := signSvc(t)
		tok := s.jobToken(func(c map[string]any) { c["email"] = testSharedSA })
		resp, body := s.signTokens(t, signBody(testDigest(t, "shared")), s.reviewRunToken(), tok, s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedJobIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedJobIdentity)
		}
		if marks := s.signMarks(t); len(marks) != 0 {
			t.Errorf("a refused caller left %v behind, want no mark at all", marks)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the class is refused before it", n)
		}
	})
}

// TestSignRouteSignsOnceAndRefusesTheSecondTime is D1074's property: one
// signature per review run, enforced by the mark and not by the caller's
// honesty. The second call is refused with the wire's existing request_refused
// — the status set is closed — and carries no signature, and the mark is still
// the first call's.
//
// A third call with a DIFFERENT digest is refused too: the mark is the run's
// and not the digest's, so a run cannot obtain a second signature by signing
// other bytes.
func TestSignRouteSignsOnceAndRefusesTheSecondTime(t *testing.T) {
	s := signSvc(t)
	digest := testDigest(t, "once")
	resp, body := s.signCall(t, signBody(digest))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first answer is %d, want 200: %s", resp.StatusCode, body)
	}
	first := signAnswer(t, body)
	mark, found := s.signMark(t, s.run)
	if !found {
		t.Fatal("the first sign left no mark")
	}

	resp, body = s.signCall(t, signBody(digest))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the second answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorRequestRefused {
		t.Errorf("the second refusal is %q, want %q", got.Error, ErrorRequestRefused)
	}
	if strings.Contains(string(body), first.Signature) {
		t.Errorf("the second answer carries the first signature: %s", body)
	}

	resp, body = s.signCall(t, signBody(testDigest(t, "once-again")))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the third answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}
	if after, _ := s.signMark(t, s.run); !after.At.Equal(mark.At) {
		t.Errorf("the mark moved: %v, want the first call's %v", after.At, mark.At)
	}

	lines := parseAuditLines(t, s.audit.String())
	if len(lines) != 3 {
		t.Fatalf("the three calls wrote %d audit lines, want three", len(lines))
	}
	for _, line := range lines {
		checkAuditShape(t, line)
		if got := line["purpose"]; got != PurposeReviewSign {
			t.Errorf("an audit line records purpose %v, want %q", got, PurposeReviewSign)
		}
	}
	if got := lines[0]["status"]; got != StatusOK {
		t.Errorf("the first sign's status is %v, want %q", got, StatusOK)
	}
	if got := lines[1]["status"]; got != ErrorRequestRefused {
		t.Errorf("the refused sign's status is %v, want %q", got, ErrorRequestRefused)
	}
	if got := lines[2]["status"]; got != ErrorRequestRefused {
		t.Errorf("the third call's status is %v, want %q", got, ErrorRequestRefused)
	}
}

// TestSecondSignIsLoggedWhereAnOperatorCanAlertOnIt: §5.6 asks for the second
// sign beside its refusal as a security event an operator can select. It is a
// warning on the service's own log rather than a field added to the audit
// line, and the line is this service's own text plus the verified names — the
// credential's NAME is not a value, and the run and the repository are the ones
// the service verified.
func TestSecondSignIsLoggedWhereAnOperatorCanAlertOnIt(t *testing.T) {
	s := signSvc(t)
	digest := testDigest(t, "alertable")
	if resp, body := s.signCall(t, signBody(digest)); resp.StatusCode != http.StatusOK {
		t.Fatalf("the first answer is %d, want 200: %s", resp.StatusCode, body)
	}
	resp, body := s.signCall(t, signBody(digest))
	if resp.StatusCode != StatusRequestRefused {
		t.Fatalf("the second answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
	}

	logs := s.logs.String()
	if !strings.Contains(logs, "second signature") {
		t.Errorf("the second sign wrote no warning an operator can select: %q", logs)
	}
	for _, want := range []string{config.ReviewSigningKeySecret(testReviewAccount), s.slug, s.run} {
		if !strings.Contains(logs, want) {
			t.Errorf("the warning does not name %q: %q", want, logs)
		}
	}
	if strings.Contains(logs, reviewKey(t, "current")) {
		t.Errorf("the warning carries the key's bytes: %q", logs)
	}
	if lines := parseAuditLines(t, s.audit.String()); len(lines) != 2 {
		t.Errorf("the two calls wrote %d audit lines, want two", len(lines))
	}
}

// TestSignRouteRefusesWhenTheMarkCannotBeTaken: the service does not sign what
// it cannot mark. The conditional write is the one the probe's lease uses, and
// the database here cannot be read (and, in the second case, cannot be written
// either) — so the route has no way to know whether the run has signed before,
// and that is a refusal, never a signature the mark would have bounded.
func TestSignRouteRefusesWhenTheMarkCannotBeTaken(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire func(*svc)
	}{
		{"the node cannot be read", func(s *svc) { s.probes.err = errStaleVersion }},
		{"the write keeps losing the node", func(s *svc) { s.probes.stale = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := signSvc(t, func(s *svc, _ *Config) { tc.wire(s) })
			resp, body := s.signCall(t, signBody(testDigest(t, "unmarkable")))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
			}
			if strings.Contains(string(body), `"signature"`) {
				t.Errorf("the refusal carries a signature: %s", body)
			}
			if marks := s.signMarks(t); len(marks) != 0 {
				t.Errorf("a sign that could not be marked left %v behind, want no mark at all", marks)
			}
			if !strings.Contains(s.logs.String(), "mark") {
				t.Errorf("the failed mark wrote no line an operator can read: %q", s.logs.String())
			}
		})
	}
}

// TestReviewRoutesNeverPrintEitherIdentityToken: the two header values this
// route verifies — the run's Firebase ID token and the job's Google ID token —
// are the caller's own tokens, and the service registers them with the
// redaction for the length of the call (D1074, §5.6). That registration is
// call.scrubs, built by readCall, and NOT WithAuditScrub: the auditor's scrub
// is armed once by the bundle loader with the document it read, and a route
// that leaned on it would be leaning on a scrub that never knew this call's
// tokens.
//
// Neither token reaches the answer, an audit line, a log line or the text of a
// refusal. The verify route's own token is registered by its own read and is
// C3.9f's test: that path is answered ahead of readCall and this one is not.
func TestReviewRoutesNeverPrintEitherIdentityToken(t *testing.T) {
	const marker = "review-route-identity-token"
	s := signSvc(t)
	runTok := s.reviewRunToken(func(c map[string]any) { c["nonce"] = marker })
	jobTok := s.reviewJobToken(func(c map[string]any) { c["nonce"] = marker })
	if runTok == s.reviewRunToken() || jobTok == s.reviewJobToken() {
		t.Fatal("the marker did not reach the tokens, so this test would prove nothing")
	}

	// A call the service serves and a call it refuses: the two paths a token
	// could be echoed on.
	served, answer := s.signTokens(t, signBody(testDigest(t, "redaction")), runTok, jobTok, s.job)
	if served.StatusCode != http.StatusOK {
		t.Fatalf("the served sign call was answered %d: %s", served.StatusCode, answer)
	}
	refused, refusalBody := s.signTokens(t, `{"digest":"nope"}`, runTok, jobTok, s.job)
	if refused.StatusCode != StatusRequestRefused {
		t.Fatalf("the refused sign call was answered %d", refused.StatusCode)
	}
	// A call refused after the class check reads the same redaction, which is
	// where the credential a refusal names is read too.
	_, codingBody := s.signTokens(t, signBody(testDigest(t, "redaction-coding")), runTok, s.jobToken(), s.job)

	for where, haystack := range map[string]string{
		"the served answer":     string(answer),
		"the refused answer":    string(refusalBody),
		"the class refusal":     string(codingBody),
		"the audit log":         s.audit.String(),
		"the service's own log": s.logs.String(),
	} {
		if strings.Contains(haystack, runTok) || strings.Contains(haystack, jobTok) {
			t.Errorf("%s carries an identity token: %s", where, haystack)
		}
	}
}

// TestSignRouteAuditsTheActAndNeverTheDigest: the line carries the verified
// side and never the caller's bytes (§5.5). The account, the repository slug
// and the run come from the identity the service verified, the credential is
// the review-signing key's bundle entry NAME, and the digest, the signature and
// the key's bytes reach no field — a field that is a caller's bytes is what the
// closed field set exists to keep out.
func TestSignRouteAuditsTheActAndNeverTheDigest(t *testing.T) {
	s := signSvc(t)
	digest := testDigest(t, "audited")
	resp, body := s.signCall(t, signBody(digest))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	sig := signAnswer(t, body).Signature

	lines := parseAuditLines(t, s.audit.String())
	if len(lines) != 1 {
		t.Fatalf("the sign wrote %d audit lines, want exactly one", len(lines))
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testReviewSA,
		"credential": config.ReviewSigningKeySecret(testReviewAccount),
		"purpose":    PurposeReviewSign,
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("%s = %v, want %q", field, got, want)
		}
	}

	raw := s.audit.String()
	for what, value := range map[string]string{
		"the digest":          digest,
		"the signature":       sig,
		"the key's bytes":     reviewKey(t, "current"),
		"the account's token": fakeCred(t, testReviewAccount),
		"the previous key":    reviewKey(t, "previous"),
		"the previous entry":  config.ReviewSigningKeyPreviousSecret(testReviewAccount),
		"the run's ID token":  s.reviewRunToken(),
		"the job's ID token":  s.reviewJobToken(),
	} {
		if strings.Contains(raw, value) {
			t.Errorf("the audit line carries %s: %s", what, raw)
		}
	}
	if s.logs.Len() != 0 {
		t.Errorf("a served sign wrote a service log line: %q", s.logs.String())
	}
}

// TestSignLineCarriesACredentialAndNoTier: the sign purpose's checked
// exemption (audit.go's credentialWithoutTier). The key a sign line names is a
// signing key and not a model credential of the cascade, so it has no tier —
// the line carries the name and no tier, and a tier on the sign line is still
// refused, because a reader would take it for a tier this service assigned.
//
// The rule it moves is read beside a case that still holds: a JOB purpose
// naming a credential without one is refused exactly as it was.
func TestSignLineCarriesACredentialAndNoTier(t *testing.T) {
	f := newFixture(t)
	id := f.verifyOK()
	name := config.ReviewSigningKeySecret(testAccount)

	a, buf := newAudit(t)
	if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: name, Purpose: PurposeReviewSign, Status: StatusOK})); err != nil {
		t.Fatalf("Write(sign): %v", err)
	}
	line := auditLines(t, buf)[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"credential": name,
		"purpose":    PurposeReviewSign,
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the sign line: %s = %v, want %q", field, got, want)
		}
	}

	t.Run("a tier on the sign line is refused", func(t *testing.T) {
		a, buf := newAudit(t)
		err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: name, Tier: TierA, Purpose: PurposeReviewSign, Status: StatusOK}))
		if err == nil {
			t.Fatal("Write accepted a tier on the sign line")
		}
		if lines := auditLines(t, buf); lines[0]["field"] != "tier" {
			t.Errorf("the refusal names field %v, want the tier", lines[0]["field"])
		}
	})

	t.Run("a job purpose still refuses a credential with no tier", func(t *testing.T) {
		a, buf := newAudit(t)
		err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: name, Purpose: PurposeForward, Status: StatusOK}))
		if err == nil {
			t.Fatal("Write accepted a forwarded call's credential with no tier")
		}
		if lines := auditLines(t, buf); lines[0]["field"] != "credential" {
			t.Errorf("the refusal names field %v, want the credential", lines[0]["field"])
		}
	})
}

// ---------------------------------------------------------------------------
// The child this package's own tests start
// ---------------------------------------------------------------------------

// signRouteChildArg marks the child of
// TestSignRouteIsUnreachableFromTheMutantEnvironment on its command line. The
// child is this test binary re-executed, and the marker travels on the command
// line because the point of the case is that its ENVIRONMENT is explicit and
// holds one variable (U7's caveat 1).
const signRouteChildArg = "sign-route-child"

// signRouteChildMarker tags the child's report line, so the parent can find it
// among the test framework's own output.
const signRouteChildMarker = "SIGN-ROUTE-CHILD-REPORT"

// signChildEnvName is the one variable the child is handed: where the service
// is. It is not a credential — it is the address of the route the child is
// meant to try — and it is the name the service's own process reads, so the
// child is started the way a review container's subprocess would be.
const signChildEnvName = "FUGARO_CREDENTIALS_SERVICE_URL"

// TestSignRouteChildFromAnExplicitEnvironment is the child half: it runs only
// when it was started with the marker, and it reports the two things the parent
// reads back — its whole environment, and what the route answered it. It writes
// to stdout rather than through t.Log, because a passing subtest's log is not
// printed and the parent reads this child's process output.
//
// The one address it is handed is the fixture's own httptest server, so the
// call is hermetic in the sense this repository means: a listener the parent
// test opened in this process, on loopback, reached by the address the test
// passed in — no DNS, no name resolution, no cloud, no installation and no
// account. That is the same server, reached the same way, as every other test
// in this package, and it is why this is not behind the `live` tag: `live`
// means what only a real installation can answer, and this case asks what this
// service's own route answers a process that holds no identity.
func TestSignRouteChildFromAnExplicitEnvironment(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 || args[0] != signRouteChildArg {
		t.Skip("this is the child of TestSignRouteIsUnreachableFromTheMutantEnvironment")
	}
	report := map[string]any{"env": os.Environ()}
	base := os.Getenv(signChildEnvName)
	if base == "" {
		fmt.Fprintf(os.Stdout, "%s %s\n", signRouteChildMarker, `{"env":[]}`)
		return
	}
	resp, err := http.Post(base+SignPath, "application/json",
		strings.NewReader(signBody(strings.Repeat("0", 64))))
	if err != nil {
		report["error"] = err.Error()
	} else {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		report["status"] = resp.StatusCode
		report["body"] = string(body)
	}
	line, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("the child's report could not be built: %v", err)
	}
	fmt.Fprintf(os.Stdout, "%s %s\n", signRouteChildMarker, line)
}

// TestSignRouteIsUnreachableFromTheMutantEnvironment: a child of this package's
// own tests must not be able to obtain a signature, and the fence it is started
// under does not cover the variables a child is HANDED at start (U7's caveat
// 1). So the child here is started from an explicit environment — an allow-list
// of the variables it needs and nothing else, which is `env -i` plus that list
// — and this test asserts the two things that follow: the child's environment
// is exactly the allow-list, and the route refuses it, because a process that
// holds none of the container's environment holds no identity to sign with.
//
// The route's unreachability from that environment is THIS task's half. The
// environment's own emptiness — that the subprocess carries no token in its
// environment at all — is CSR4b's, held by
// TestMutationSubprocessCarriesNoTokenInItsEnvironment
// (docs/cloud-senior-review/implementation-plan.md); this test starts its child
// the way that one does and asserts what its own route owes. It is also why the
// child is not fenced here: `unshare -rn` removes the loopback the fixture's own
// server listens on, so a fenced child could not try the route at all.
func TestSignRouteIsUnreachableFromTheMutantEnvironment(t *testing.T) {
	s := signSvc(t)
	allowList := []string{signChildEnvName + "=" + s.http.URL}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("this test binary's own path could not be read: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSignRouteChildFromAnExplicitEnvironment$", signRouteChildArg)
	// The environment is SET to the allow-list rather than inherited, and that
	// assignment IS the clearing: os/exec hands the child exactly this slice and
	// nothing else, so the child of `env -i <allow-list>` and the child of this
	// line are the same child. Leaving it nil is the other state — the child
	// carries this process's own environment, which is exactly what U7's caveat
	// 1 is about.
	//
	// PATH is deliberately NOT on the list, and the child still starts: the
	// parent resolves the binary's own path here, and a dynamically linked
	// binary's loader reads the program's own search path and the system cache,
	// not PATH or LD_LIBRARY_PATH. Adding PATH would put a variable on the list
	// the pin then carries for no reason — and the pin's value is that the list
	// is exactly what this child needs.
	cmd.Env = allowList
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the child did not run: %v\n%s", err, out)
	}

	report := childReport(t, string(out))
	env, ok := report["env"].([]any)
	if !ok {
		t.Fatalf("the child reported no environment: %v", report)
	}
	if len(env) != len(allowList) {
		t.Errorf("the child's environment holds %d variables %v, want exactly the allow-list %v", len(env), env, allowList)
	}
	for _, want := range allowList {
		if !slices.Contains(env, any(want)) {
			t.Errorf("the child's environment does not hold %q: %v", want, env)
		}
	}
	if _, reached := report["error"]; reached {
		t.Fatalf("the child could not reach the route at all: %v", report["error"])
	}

	if got := report["status"]; got != float64(StatusUnverified) {
		t.Errorf("the child was answered %v, want %d: a process that holds no identity signs nothing", got, StatusUnverified)
	}
	body, _ := report["body"].(string)
	if strings.Contains(body, `"signature"`) {
		t.Errorf("the child was handed a signature: %s", body)
	}
	if !strings.Contains(body, ErrorUnverifiedRunIdentity) {
		t.Errorf("the child's refusal is %q, want the contract's %q", body, ErrorUnverifiedRunIdentity)
	}
	if marks := s.signMarks(t); len(marks) != 0 {
		t.Errorf("a refused child left %v behind, want no mark at all", marks)
	}
}

// childReport reads the child's marked report line.
func childReport(t *testing.T, out string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), signRouteChildMarker+" ")
		if !ok {
			continue
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(rest), &report); err != nil {
			t.Fatalf("the child's report is not JSON: %v (%q)", err, rest)
		}
		return report
	}
	t.Fatalf("the child printed no report: %s", out)
	return nil
}

// ---------------------------------------------------------------------------
// The verify route (C3.9f)
// ---------------------------------------------------------------------------

// testVerifierSA is the address the fixture's verifier list holds. It is a bare
// address and not an IAM member spelling, because that is what the list is
// matched against: the `email` claim of the caller's Google ID token. It is a
// name and not a credential.
var testVerifierSA = "coordinator@aurora-dev.iam.gserviceaccount.com"

// verifySvc is the fixture behind a verifier: an installation whose verifier
// list holds the addresses the test gives, a bundle holding the account's
// review-signing key, and the review class configured exactly as the sign
// route's fixture configures it — so the only thing a verify call is missing is
// the run identity no verifier has.
func verifySvc(t *testing.T, verifiers []string, extra ...func(*svc, *Config)) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			SharedJobAccounts: []string{testSharedSA},
			ReviewJobAccounts: []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			VerifierAccounts:  verifiers,
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
		cfg.Repos = map[string]string{s.slug: testRepo}
		s.src.docs = [][]byte{reviewSigningDoc(t)}
		cfg.Signs = s.probes
		for _, m := range extra {
			m(s, cfg)
		}
	})
}

// verifierToken is the caller's Google ID token as the verifier's own address.
func verifierToken(t *testing.T, s *svc, email string, mutate ...func(map[string]any)) string {
	t.Helper()
	return s.jobToken(append([]func(map[string]any){
		func(c map[string]any) { c["email"] = email },
	}, mutate...)...)
}

// verifyBody is a verify call's body: the frame's three fields and the
// signature the service is asked about.
func verifyBody(account, run, repository, digest, signature string) string {
	return `{"account":"` + account + `","digest":"` + digest + `","signature":"` + signature +
		`","run":"` + run + `","repository":"` + repository + `"}`
}

// verifyCall sends one verify call as the fixture's verifier and reads the
// answer.
func (s *svc) verifyCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.verifyTokens(t, body, verifierToken(t, s, testVerifierSA), mutate...)
}

// verifyTokens is verifyCall with the caller's own token given rather than
// minted, for the calls that must present one the service refuses.
func (s *svc) verifyTokens(t *testing.T, body, jobTok string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	resp := s.postVerify(t, body, jobTok, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// postVerify sends one verify call: the verifier's Google ID token in
// Authorization and NOTHING else — no run identity, because a verifier holds
// none, and no reported job name, because a verifier is not a job of this
// installation. That is the whole shape of the call the class is designed for,
// and a route left behind the shared parse refuses it before the class is ever
// consulted.
func (s *svc) postVerify(t *testing.T, body, jobTok string, mutate ...func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+VerifyPath, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	if jobTok != "" {
		req.Header.Set("Authorization", "Bearer "+jobTok)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	return resp
}

// verifyAnswer decodes a verify answer strictly and holds it to its one field:
// the answer is the one refusal bit and nothing else — no reason, no key id and
// no echo of the account (spec §5.3).
func verifyAnswer(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var ans map[string]any
	if err := decodeStrict(body, &ans); err != nil {
		t.Fatalf("the verify answer is not one this contract can read: %v (%s)", err, body)
	}
	if len(ans) != 1 {
		t.Fatalf("the verify answer carries %d fields, want the one bit: %s", len(ans), body)
	}
	valid, ok := ans["valid"].(bool)
	if !ok {
		t.Fatalf("the verify answer's one field is not the bit: %s", body)
	}
	return map[string]any{"valid": valid}
}

// verifyValid reads the one bit out of an answer, failing when it is not the
// one the test asked for.
func verifyValid(t *testing.T, resp *http.Response, body []byte, want bool) {
	t.Helper()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	if got := verifyAnswer(t, body)["valid"]; got != want {
		t.Errorf("valid = %v, want %v: %s", got, want, body)
	}
}

// TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne is C3.9f's
// red step: the route recomputes the HMAC over the FRAME the caller names —
// C3.9e's builder, so the verify route reads the bytes the sign route wrote —
// and answers the one bit. Before this task the path is not in ServeHTTP's set
// at all, so this call is the service's 404.
//
// The two answers come from the same call shape, which is the point: `true` is
// "the service recomputed the HMAC and it is this account's", `false` is "it
// recomputed and it is not", and neither is a refusal.
func TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "verify")
	mac := hmac.New(sha256.New, signKey(t))
	mac.Write([]byte(frameVersion + "\n" + s.slug + "\n" + s.run + "\n" + digest))
	signature := hex.EncodeToString(mac.Sum(nil))

	resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, signature))
	verifyValid(t, resp, body, true)

	tampered := verifyBody(testReviewAccount, s.run, s.slug, digest, strings.Repeat("0", 64))
	resp, body = s.verifyCall(t, tampered)
	verifyValid(t, resp, body, false)
}

// TestVerifyRouteRefusesAnEmptyVerifierList: the list is configuration and it
// is empty by default, so an installation with no coordinator has no reachable
// route — and the refusal is the CALLER CLASS's own typed one rather than the
// 404 a path outside ServeHTTP's set gets, because a caller must be able to
// tell "you may not ask" from "there is nothing here".
func TestVerifyRouteRefusesAnEmptyVerifierList(t *testing.T) {
	s := verifySvc(t, nil)
	digest := testDigest(t, "empty-list")
	resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, strings.Repeat("0", 64)))
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedJobIdentity)
	}
	if s.audit.Len() != 0 {
		t.Errorf("a caller no verifier list admits wrote an audit line: %q", s.audit.String())
	}
}

// signFor is the signature the sign route would have answered for one run of
// one repository: the HMAC of C3.9e's framed message under the fixture's
// review-signing key. A test that verifies a signature recomputes it from the
// three parts rather than taking it from the service, so what is checked is the
// frame's own bytes.
func signFor(t *testing.T, slug, run, digest string) string {
	t.Helper()
	mac := hmac.New(sha256.New, signKey(t))
	mac.Write([]byte(frameVersion + "\n" + slug + "\n" + run + "\n" + digest))
	return hex.EncodeToString(mac.Sum(nil))
}

// TestVerifyRouteAnswersATypedRefusalWhenTheKeyIsUnreadable: the distinction
// §5.3 exists for. An account this installation holds no review-signing entry
// for, and a bundle the service cannot read at all, are answers the service
// CANNOT GIVE — and a bare `false` would tell a coordinator that a verdict is
// not ours when the truth is that we could not tell. So both are typed
// refusals, and a coordinator reads both as "do not ship" without ever reading
// them as "the artifact is not that account's".
func TestVerifyRouteAnswersATypedRefusalWhenTheKeyIsUnreadable(t *testing.T) {
	t.Run("an account with no key", func(t *testing.T) {
		s := verifySvc(t, []string{testVerifierSA})
		resp, body := s.verifyCall(t, verifyBody("another-account", s.run, s.slug, testDigest(t, "no-key"), strings.Repeat("0", 64)))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
		if strings.Contains(string(body), `"valid"`) {
			t.Errorf("an unreadable key was answered with the bit: %s", body)
		}
		lines := parseAuditLines(t, s.audit.String())
		if len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one", len(lines))
		}
		if got := lines[0]["status"]; got != ErrorStateUnavailable {
			t.Errorf("the line's status is %v, want %q", got, ErrorStateUnavailable)
		}
		if got := lines[0]["credential"]; got != "" {
			t.Errorf("the line names credential %v, want none: no entry was chosen", got)
		}
	})

	t.Run("a bundle that cannot be read", func(t *testing.T) {
		s := verifySvc(t, []string{testVerifierSA})
		s.src.docs = [][]byte{[]byte("{not json")}
		resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, testDigest(t, "unreadable"), strings.Repeat("0", 64)))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
	})
}

// TestVerifyRouteRefusesAnAccountTheCallerMayNotAskAbout: the account a verify
// call names is held to the rule a bundle entry's NAME is held to (spec §5.3),
// and a name that could not be one is `request_refused` — "your call was bad" —
// and never `valid:false`, which keeps the bit's one meaning: the service
// recomputed the HMAC and it differs.
func TestVerifyRouteRefusesAnAccountTheCallerMayNotAskAbout(t *testing.T) {
	for _, tc := range []struct{ name, account string }{
		{"empty", ""},
		{"a name with a space", "two words"},
		{"a name with a newline", "line\nbreak"},
		{"a name over the bundle's bound", strings.Repeat("a", maxCredentialNameBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := verifySvc(t, []string{testVerifierSA})
			resp, body := s.verifyCall(t, verifyBody(tc.account, s.run, s.slug, testDigest(t, "account"), strings.Repeat("0", 64)))
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want none: a name this contract refuses reads no key", n)
			}
		})
	}
}

// TestVerifyRouteDoesNotEchoTheAccountOrAKeyID: the answer is the one bit and
// NOTHING else — no reason, no key id, no account echo, and no distinguisher
// between "wrong key" and "wrong bytes" (§5.3). The account is the caller's own
// text and the key's entry name is derived from it, so a route that echoed
// either would be handing back a name it was asked about and, worse, telling a
// caller which entry the service looked for.
func TestVerifyRouteDoesNotEchoTheAccountOrAKeyID(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	account := "another-account"
	digest := testDigest(t, "no-echo")

	for _, call := range []struct {
		name string
		body string
	}{
		{"a served call", verifyBody(account, s.run, s.slug, digest, strings.Repeat("0", 64))},
		{"a refused call", verifyBody(account, s.run, s.slug, digest, "not-a-signature")},
	} {
		t.Run(call.name, func(t *testing.T) {
			resp, body := s.verifyCall(t, call.body)
			haystack := string(body) + s.audit.String() + s.logs.String()
			for what, value := range map[string]string{
				"the account":            account,
				"the key's entry name":   config.ReviewSigningKeySecret(account),
				"the previous entry":     config.ReviewSigningKeyPreviousSecret(account),
				"the account of the key": config.ReviewSigningKeySecret(testReviewAccount),
			} {
				if strings.Contains(haystack, value) {
					t.Errorf("the %s (%d) carries %s: %s", call.name, resp.StatusCode, what, haystack)
				}
			}
		})
	}
}

// TestVerifyRouteRefusesASignatureMadeForAnotherRunOrRepository: the frame is
// what makes a signature valid for exactly one run of one repository (D1073),
// and the verify route refuses a cross-run or cross-repository replay BY ITSELF
// — the service recomputes the HMAC over the frame the caller names, so naming
// different bytes is `valid:false` and not a match. No coordinator check is
// involved: this is the service's own answer about its own signature.
func TestVerifyRouteRefusesASignatureMadeForAnotherRunOrRepository(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "replay")
	signature := signFor(t, s.slug, s.run, digest)

	resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, signature))
	verifyValid(t, resp, body, true)

	for _, tc := range []struct{ name, run, repository string }{
		{"another run of the same repository", "20261102-120000-ffff", s.slug},
		{"another repository", s.run, "aurora-other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := s.verifyCall(t, verifyBody(testReviewAccount, tc.run, tc.repository, digest, signature))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the answer is %d, want the one bit: %s", resp.StatusCode, body)
			}
			verifyValid(t, resp, body, false)
		})
	}

	t.Run("the digest is bound too", func(t *testing.T) {
		resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, testDigest(t, "another"), signature))
		verifyValid(t, resp, body, false)
	})
}

// TestVerifyRouteIsAnsweredAheadOfTheSharedParse: the ordering is the whole of
// why the class exists (spec §5.4). RequestFromHTTP reads the two identity
// headers and Verify refuses a call with no run identity BEFORE ServeHTTP
// branches on the path — and a verifier holds no run identity, so a route left
// behind that parse refuses every verifier before the class is ever consulted,
// and the whole route is dead configuration.
//
// The call below carries the verifier's Google ID token and NOTHING else: no
// run identity, no reported job name. It is answered. The contrast case is the
// same omission on a route the shared parse does reach, which is still refused
// exactly as it was.
func TestVerifyRouteIsAnsweredAheadOfTheSharedParse(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "ordering")
	body := verifyBody(testReviewAccount, s.run, s.slug, digest, signFor(t, s.slug, s.run, digest))

	resp, answer := s.verifyCall(t, body)
	verifyValid(t, resp, answer, true)

	// The same call, with the two headers the shared parse wants filled in with
	// a run identity that is not a verifier's: it is still the class's answer,
	// because the class is the whole of this route's admission.
	resp, answer = s.verifyCall(t, body, func(r *http.Request) {
		r.Header.Set(HeaderRunIdentity, s.runToken())
		r.Header.Set(HeaderRunJob, s.job)
	})
	verifyValid(t, resp, answer, true)

	t.Run("a route the shared parse reaches still needs a run identity", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, s.http.URL+SignPath, strings.NewReader(signBody(digest)))
		if err != nil {
			t.Fatalf("building the call: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+verifierToken(t, s, testVerifierSA))
		resp, err := s.http.Client().Do(req)
		if err != nil {
			t.Fatalf("the call failed: %v", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading the answer: %v", err)
		}
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, raw)
		}
		if got := refusal(t, resp.StatusCode, raw); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
	})
}

// TestVerifyComparesTheSignatureInConstantTime: F7's rule. The signature is an
// attacker-supplied value, so the comparison must not stop at the first
// differing byte — a `==` on the hex strings, or a byte loop of the caller's
// own, would let a caller learn a valid prefix from how long the answer took.
//
// The test wraps the comparison the route makes and asserts it is reached with
// two 32-byte MACs, which is what makes hmac.Equal's guarantee apply: it is
// constant-time for equal lengths. A same-length wrong signature is then a plain
// `valid:false`, and the wrapper delegates to hmac.Equal, so what is observed is
// the route's behaviour and not a comment about it.
func TestVerifyComparesTheSignatureInConstantTime(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "constant-time")

	var (
		mu      sync.Mutex
		lengths [][2]int
	)
	original := verifyEqual
	verifyEqual = func(a, b []byte) bool {
		mu.Lock()
		lengths = append(lengths, [2]int{len(a), len(b)})
		mu.Unlock()
		return hmac.Equal(a, b)
	}
	t.Cleanup(func() { verifyEqual = original })

	resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, signFor(t, s.slug, s.run, digest)))
	verifyValid(t, resp, body, true)

	// A signature of exactly the shape and length a real one has, and wrong: it
	// is a `valid:false` and not a refusal, so it reaches the comparison.
	wrong := strings.Repeat("a", 64)
	resp, body = s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, wrong))
	verifyValid(t, resp, body, false)

	mu.Lock()
	defer mu.Unlock()
	if len(lengths) != 2 {
		t.Fatalf("the comparison was reached %d times, want once per served call", len(lengths))
	}
	for _, l := range lengths {
		if l[0] != sha256.Size || l[1] != sha256.Size {
			t.Errorf("the comparison was handed %d and %d bytes, want two %d-byte MACs", l[0], l[1], sha256.Size)
		}
	}
}

// TestVerifyRouteRefusesACallerOutsideTheVerifierList: the list is the ROUTE's
// admission and not the installation's job set. An address that is one of the
// installation's job service accounts — even one whose review class is
// configured — is not a verifier until it is written into this list, and the
// refusal is the class's own typed one, with no audit line, because nothing was
// verified.
func TestVerifyRouteRefusesACallerOutsideTheVerifierList(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "outside")
	body := verifyBody(testReviewAccount, s.run, s.slug, digest, signFor(t, s.slug, s.run, digest))

	resp, answer := s.verifyTokens(t, body, s.jobToken())
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, answer)
	}
	if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedJobIdentity)
	}
	if s.audit.Len() != 0 {
		t.Errorf("a caller the list does not hold wrote an audit line: %q", s.audit.String())
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: nothing is verified yet", n)
	}
}

// TestReviewAccountCannotUseTheVerifyRoute: the two classes are two lists on
// purpose. The fixture's review job account is configured as a review caller —
// it may sign and it may call the model path — and it may NOT verify: a
// verifier's answer is about a verdict the coordinator launched, and an account
// that produced a verdict must not be the one that answers whether it is
// genuine. The class is the route's own gate, so this is the same refusal a
// stranger's address gets.
func TestReviewAccountCannotUseTheVerifyRoute(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "review-account")
	body := verifyBody(testReviewAccount, s.run, s.slug, digest, signFor(t, s.slug, s.run, digest))

	resp, answer := s.verifyTokens(t, body, s.reviewJobToken())
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, answer)
	}
	if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorUnverifiedJobIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedJobIdentity)
	}
	if s.audit.Len() != 0 {
		t.Errorf("a review account's verify attempt wrote an audit line: %q", s.audit.String())
	}
}

// TestVerifyRouteRefusesAnUnknownFieldAndAnOversizeBody: the body decodes
// strictly and is bounded, as every body of this contract is (Convention 6).
// The bound is checked before the bundle is read, so a body that is not a verify
// call reads no key.
func TestVerifyRouteRefusesAnUnknownFieldAndAnOversizeBody(t *testing.T) {
	t.Run("an unknown field", func(t *testing.T) {
		s := verifySvc(t, []string{testVerifierSA})
		body := `{"account":"` + testReviewAccount + `","digest":"` + testDigest(t, "unknown") +
			`","signature":"` + strings.Repeat("0", 64) + `","run":"r","repository":"s","alg":"MD5"}`
		resp, answer := s.verifyCall(t, body)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
		}
		if got := refusal(t, resp.StatusCode, answer); got.Error != ErrorRequestRefused {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorRequestRefused)
		}
	})

	t.Run("an oversize body", func(t *testing.T) {
		s := verifySvc(t, []string{testVerifierSA})
		big := `{"account":"` + testReviewAccount + `","digest":"` + testDigest(t, "oversize") +
			`","signature":"` + strings.Repeat("0", 64) + `","run":"r","repository":"s","x":"` +
			strings.Repeat("a", maxMintRequestBytes) + `"}`
		resp, answer := s.verifyCall(t, big)
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the bound is checked before a key is read", n)
		}
	})

	t.Run("a signature that is not 64 lowercase hex", func(t *testing.T) {
		s := verifySvc(t, []string{testVerifierSA})
		resp, answer := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, testDigest(t, "short"), "ABCDEF"))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, answer)
		}
		if strings.Contains(string(answer), `"valid"`) {
			t.Errorf("a malformed signature was answered with the bit rather than refused: %s", answer)
		}
	})
}

// TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction: §5.6's rule for the
// one route the shared parse never reaches. readCall builds the call-scoped
// scrub the model path and the sign route use, and it is requested by THIS
// handler with the one Google ID token the verifier carries, and with that one
// only: the shared parse that would have gathered it never runs for this path.
//
// Neither the verifier's token nor its signature reaches the answer, the audit
// line, a log line or the text of a refusal. TestReviewRoutesNeverPrintEitherIdentityToken
// is not a substitute: it drives the routes the shared parse does reach, which
// is why this route needs one of its own.
func TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction(t *testing.T) {
	const marker = "verify-route-identity-token"
	s := verifySvc(t, []string{testVerifierSA})
	tok := verifierToken(t, s, testVerifierSA, func(c map[string]any) { c["nonce"] = marker })
	if tok == verifierToken(t, s, testVerifierSA) {
		t.Fatal("the marker did not reach the token, so this test would prove nothing")
	}
	digest := testDigest(t, "redaction")
	signature := signFor(t, s.slug, s.run, digest)

	// A call the service serves, a call it refuses for what was sent, and a
	// call it cannot answer — the three paths an echo could happen on. The
	// refused one carries the token AS the account name, which is the one field
	// a caller controls that the service reads and then reports on.
	served, answer := s.verifyTokens(t, verifyBody(testReviewAccount, s.run, s.slug, digest, signature), tok)
	if served.StatusCode != http.StatusOK {
		t.Fatalf("the served verify call was answered %d: %s", served.StatusCode, answer)
	}
	refused, refusalBody := s.verifyTokens(t, verifyBody(tok, s.run, s.slug, digest, signature), tok)
	if refused.StatusCode != StatusUnavailable {
		t.Fatalf("the refused verify call was answered %d", refused.StatusCode)
	}
	_, malformed := s.verifyTokens(t, `{"account":`, tok)

	for where, haystack := range map[string]string{
		"the served answer":     string(answer),
		"the refusal":           string(refusalBody),
		"the malformed refusal": string(malformed),
		"the audit log":         s.audit.String(),
		"the service's own log": s.logs.String(),
	} {
		if strings.Contains(haystack, tok) {
			t.Errorf("%s carries the verifier's identity token: %s", where, haystack)
		}
		if strings.Contains(haystack, signature) {
			t.Errorf("%s carries the signature: %s", where, haystack)
		}
	}
}

// TestVerifyRouteAuditsTheActAndNeverTheDigestOrTheSignature: the line carries
// the verifier as its caller and the review-signing key as its credential, and
// neither a run nor a job (§5.5) — a verifier holds no run identity and is not a
// job of this installation, so the act is the purpose, the credential and the
// status. The digest, the signature and the key's bytes reach no field: a field
// that is a caller's bytes is what the closed field set exists to keep out, and
// a line that carried the signature would invite one to carry the key.
func TestVerifyRouteAuditsTheActAndNeverTheDigestOrTheSignature(t *testing.T) {
	s := verifySvc(t, []string{testVerifierSA})
	digest := testDigest(t, "audited")
	signature := signFor(t, s.slug, s.run, digest)

	resp, body := s.verifyCall(t, verifyBody(testReviewAccount, s.run, s.slug, digest, signature))
	verifyValid(t, resp, body, true)

	lines := parseAuditLines(t, s.audit.String())
	if len(lines) != 1 {
		t.Fatalf("the verify wrote %d audit lines, want exactly one", len(lines))
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]string{
		"run":        "",
		"repository": "",
		"job":        "",
		"caller":     testVerifierSA,
		"credential": config.ReviewSigningKeySecret(testReviewAccount),
		"purpose":    PurposeReviewVerify,
		"tier":       "",
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("%s = %v, want %q", field, got, want)
		}
	}

	raw := s.audit.String()
	for what, value := range map[string]string{
		"the digest":           digest,
		"the signature":        signature,
		"the key's bytes":      reviewKey(t, "current"),
		"the previous key":     reviewKey(t, "previous"),
		"the previous entry":   config.ReviewSigningKeyPreviousSecret(testReviewAccount),
		"the account's token":  fakeCred(t, testReviewAccount),
		"the verifier's token": verifierToken(t, s, testVerifierSA),
	} {
		if strings.Contains(raw, value) {
			t.Errorf("the audit line carries %s: %s", what, raw)
		}
	}
	if s.logs.Len() != 0 {
		t.Errorf("a served verify wrote a service log line: %q", s.logs.String())
	}
}
