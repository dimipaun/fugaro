package credsvc

// What this file is: the owner's cascade halt of 2026-11-14, rebuilt from the
// installation's own configuration. Every run of a cascade repository stopped
// at its first model call with 503 state_unavailable and a refusal that named
// nothing, and the answer was in the published policy all along: O4's
// admission offers a repository the INTERSECTION of its own node's accounts,
// each account's allow_repos and the project's allow_repos, and the owner's
// contained no account the bundle could serve the call from — so the list the
// selector was handed was empty and creds.Select answered ErrNoCandidates.
//
// The shape below is that installation: the bundle holds the named account's
// oauth credential and nothing else (a repository whose runs are the cascade
// under `oauth-only` has no paid key to fall back to), the shared node offers
// that account to one repository, and the repository's own node is the
// admission that decides whether it reaches the call. Each test here is one
// answer to "why was this repository served nothing", and none of them is a
// credential the repository may not use: the admission is the security
// property and it is unchanged.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// ownerAccount is the one Claude account this installation's bundle holds and
// its repository's own configuration names, standing where the owner's named
// account stood.
const ownerAccount = "belong"

// ownerShape is the installation the halt happened under: one named account in
// the bundle, the shared node's accounts block as accountsBlock, the
// repository's own node as child, and the repository's recorded auth the
// cascade — which is what puts a run of it through this service at all (B2.1).
func ownerShape(t *testing.T, accountsBlock, child string) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(config.ClaudeOAuthTokenPrefix + ownerAccount + ": " + fakeCred(t, ownerAccount) + "\n")}
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto", accountsBlock))
		s.policy.admissions[s.slug] = decodeTestChild(t, child)
		s.state.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: config.AuthCascade})
	})
}

// ownerAccounts is the shared node's accounts block with the named account
// offered to the repositories named.
func ownerAccounts(allow ...string) string {
	quoted := make([]string, 0, len(allow))
	for _, r := range allow {
		quoted = append(quoted, `"`+r+`"`)
	}
	return `"` + ownerAccount + `": {"allow_repos": [` + strings.Join(quoted, ", ") + `]}`
}

// TestCascadeOwnerShapeIsServedWhenAdmitted is the control the refusals below
// are read against: the same installation with the admission covering the
// account serves the call with it, so a refusal there is the admission's
// answer and not a fixture that cannot serve anything at all.
func TestCascadeOwnerShapeIsServedWhenAdmitted(t *testing.T) {
	s := ownerShape(t, ownerAccounts(testRepo), testChildDoc(ownerAccount))

	resp, body := s.call(t, callBody("admitted"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != ownerAccount {
		t.Errorf("the call was served by %q, want %q", got, ownerAccount)
	}
}

// TestCascadeNoCandidateCredentialsIsItsOwnRefusal pins the refusal a
// repository admitted no credential gets, on both legs of the admission that
// can produce it. The class is the assertion that matters most: state_unavailable
// says the STATE could not be read and sends the operator to the database, and
// what actually happened is that the state was read and the policy admitted
// this repository nothing.
func TestCascadeNoCandidateCredentialsIsItsOwnRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		// what is the leg of O4's admission this case removes the credential by.
		what string
		// accountsBlock is the shared node's accounts block, child the
		// repository's own node.
		accountsBlock, child string
	}{
		{
			name:          "the repository's own node does not name the account",
			what:          "the repository's own node's credentials.accounts",
			accountsBlock: ownerAccounts(testRepo),
			child:         testChildDoc(config.DefaultClaudeAccount),
		},
		{
			name:          "the shared node does not offer the account to this repository",
			what:          "the account's allow_repos on the shared node",
			accountsBlock: ownerAccounts(testOtherRepo),
			child:         testChildDoc(ownerAccount),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ownerShape(t, tc.accountsBlock, tc.child)

			resp, body := s.call(t, callBody("no-candidates"))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the refusal answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorNoCandidateCredentials {
				t.Errorf("the refusal is %q, want %q: %q says the state could not be read, and it was (%s)",
					e.Error, ErrorNoCandidateCredentials, ErrorStateUnavailable, tc.what)
			}
			if !strings.Contains(e.Message, "the state was read") {
				t.Errorf("the refusal does not say the state was read: %q", e.Message)
			}
			// What was considered, and the one filter that removed it: the
			// run's own answer, which is also what its halt is diagnosed from.
			if len(e.Tried) != 1 || e.Tried[0].Name != ownerAccount ||
				e.Tried[0].Tier != TierA || e.Tried[0].Reason != ReasonNotAdmitted {
				t.Errorf("the refusal's tried list is %+v, want one entry naming %q, tier A, %q",
					e.Tried, ownerAccount, ReasonNotAdmitted)
			}
			// The security property this refusal is the other side of: a
			// credential the repository is not admitted is not offered, and
			// nothing of the call reaches the upstream.
			if got := resp.Header.Get(HeaderCredential); got != "" {
				t.Errorf("a refused call carries the credential header %q", got)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d calls, want none: no credential this repository may use exists", n)
			}
			// The operator's own line, which is what a halt like this one is
			// diagnosed from: the class, the cause and the same list.
			log := s.logs.String()
			for _, want := range []string{
				"type=" + ErrorNoCandidateCredentials,
				"tried=\"" + ownerAccount + " (" + ReasonNotAdmitted + ")\"",
			} {
				if !strings.Contains(log, want) {
					t.Errorf("the refusal line does not carry %q: %s", want, log)
				}
			}
			// One audited act per refusal (C1.13), with the refusal's own type
			// as its status — the class the operator greps for.
			lines := parseAuditLines(t, s.audit.String())
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
			}
			checkAuditShape(t, lines[0])
			if got := lines[0]["status"]; got != ErrorNoCandidateCredentials {
				t.Errorf("the audit line's status is %v, want %q", got, ErrorNoCandidateCredentials)
			}
		})
	}
}

// TestCascadeNoCandidatesNamesAnEmptyBundle: the other way to a call with
// nothing to select from, and the one no admission is consulted for — a bundle
// that holds no model credential at all. The sentence has to say that rather
// than send the operator to the repository's own policy, because the policy of
// a repository is not what is missing.
func TestCascadeNoCandidatesNamesAnEmptyBundle(t *testing.T) {
	indented := "  " + strings.ReplaceAll(strings.TrimRight(appKeyPEM(t), "\n"), "\n", "\n  ")
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(config.GitHubAppKeySecret + ": |\n" + indented + "\n")}
		s.policy.shared = decodeTestPolicy(t, testPolicyDoc())
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(ownerAccount))
		s.state.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: config.AuthCascade})
	})

	resp, body := s.call(t, callBody("empty-bundle"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the refusal answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorNoCandidateCredentials {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorNoCandidateCredentials)
	}
	if !strings.Contains(e.Message, "bundle holds no model credential") {
		t.Errorf("the refusal does not name the empty bundle: %q", e.Message)
	}
	if len(e.Tried) != 0 {
		t.Errorf("the refusal carries a tried list of %+v, want none: nothing was dropped, nothing was there", e.Tried)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d calls, want none", n)
	}
}

// TestCascadeNoCandidatesAtTheListBound: the refusal names every credential it
// considered, at the largest list the contract carries. The bound is what this
// pins on both sides — the bundle may hold maxTriedEntries credentials and a
// refusal may name exactly that many, one more is refused by the writer's own
// reader and answered as state_unavailable, which is the mis-diagnosis this
// refusal exists to end — and it is why the sentence needs no clipping: an
// account's name is at most config.MaxClaudeAccountNameLen bytes, so the whole
// rendering of a full list stays far inside maxRefusalMessageBytes.
func TestCascadeNoCandidatesAtTheListBound(t *testing.T) {
	names := make([]string, 0, maxTriedEntries)
	var doc strings.Builder
	for i := 0; i < maxTriedEntries; i++ {
		name := fmt.Sprintf("acct-%02d", i)
		names = append(names, name)
		doc.WriteString(config.ClaudeOAuthTokenPrefix + name + ": " + fakeCred(t, name) + "\n")
	}
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(doc.String())}
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto", ownerAccounts(testRepo)))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount))
		s.state.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: config.AuthCascade})
	})

	resp, body := s.call(t, callBody("full-list"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the refusal answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorNoCandidateCredentials {
		t.Fatalf("the refusal is %q, want %q: a list at the contract's own bound must not fall back to it",
			e.Error, ErrorNoCandidateCredentials)
	}
	if len(e.Tried) != maxTriedEntries {
		t.Fatalf("the refused call carries a tried list of %d, want all %d of them", len(e.Tried), maxTriedEntries)
	}
	seen := make(map[string]bool, len(e.Tried))
	for i, tr := range e.Tried {
		if tr.Name != names[i] || tr.Reason != ReasonNotAdmitted {
			t.Errorf("tried[%d] is %+v, want %q (not_admitted)", i, tr, names[i])
		}
		seen[tr.Name] = true
	}
	if len(seen) != maxTriedEntries {
		t.Errorf("the tried list names %d distinct credentials, want %d", len(seen), maxTriedEntries)
	}
	if len(e.Message) > maxRefusalMessageBytes {
		t.Errorf("the refusal's message is %d bytes, over the contract's bound of %d", len(e.Message), maxRefusalMessageBytes)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d calls, want none", n)
	}
}

// TestCascadeNoCandidatesOnTheRecordedAuthNamesTheHalf: the second way a
// repository whose record is not the cascade reaches a call with nothing to
// select from, and the one the owner's installation of 2026-11-24 hit — the
// record says oauth and names an account, and the list the admission left holds
// nothing of that account's. The two states it can be are different fixes: an
// entry the bundle does not hold is the installation's, and one the admission
// dropped is the repository's own policy's. The refusal used to answer both
// with one sentence and the state_unavailable class, which is what sent the
// owner to the database while the answer was in the bundle.
func TestCascadeNoCandidatesOnTheRecordedAuthNamesTheHalf(t *testing.T) {
	for _, tc := range []struct {
		name string
		// docs is the bundle: the account's own token, or the default
		// account's where the installation never entered it.
		docs string
		// child is the repository's own node: the leg of O4's admission this
		// case removes the account by.
		child string
		// want is the half the refusal has to name, and not the other, and
		// dropped is the credential the bundle held and the walk left out.
		want, dropped string
	}{
		{
			name:    "the bundle holds the account and the admission does not name it",
			docs:    config.ClaudeOAuthTokenPrefix + ownerAccount + ": " + fakeCred(t, ownerAccount) + "\n",
			child:   testChildDoc(config.DefaultClaudeAccount),
			want:    "the repository's own admission does not name it",
			dropped: ownerAccount,
		},
		{
			name:    "the bundle holds no credential for the account",
			docs:    config.ClaudeOAuthToken + ": " + fakeCred(t, config.DefaultClaudeAccount) + "\n",
			child:   testChildDoc(ownerAccount),
			want:    "holds no credential for that account",
			dropped: config.DefaultClaudeAccount,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvcWith(t, func(s *svc, _ *Config) {
				s.src.docs = [][]byte{[]byte(tc.docs)}
				s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto", ownerAccounts(testRepo)))
				s.policy.admissions[s.slug] = decodeTestChild(t, tc.child)
				s.state.setRepoAuth(s.slug, creds.RepoAuth{
					Version: creds.RepoAuthVersion, Auth: config.AuthOAuth, Accounts: []string{ownerAccount},
				})
			})

			resp, body := s.call(t, callBody("oauth-empty"), declaredCall(config.AuthOAuth, ownerAccount))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the refusal answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != ErrorNoCandidateCredentials {
				t.Errorf("the refusal is %q, want %q: the state was read, and the answer is in the bundle and the policy",
					e.Error, ErrorNoCandidateCredentials)
			}
			if !strings.Contains(e.Message, tc.want) {
				t.Errorf("the refusal does not name the half it is: %q", e.Message)
			}
			if other := "holds no credential for that account"; tc.want != other && strings.Contains(e.Message, other) {
				t.Errorf("the refusal names the bundle for a call the admission refused: %q", e.Message)
			}
			// What was considered, and the one filter that removed it: the
			// run's own answer, which is also what its halt is diagnosed from.
			if len(e.Tried) != 1 || e.Tried[0].Name != tc.dropped ||
				e.Tried[0].Tier != TierA || e.Tried[0].Reason != ReasonNotAdmitted {
				t.Errorf("the refusal's tried list is %+v, want one entry naming %q, tier A, %q",
					e.Tried, tc.dropped, ReasonNotAdmitted)
			}
			// The security property this refusal is the other side of: nothing
			// of the call reaches the upstream, and no value reaches a line.
			if got := resp.Header.Get(HeaderCredential); got != "" {
				t.Errorf("a refused call carries the credential header %q", got)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d calls, want none", n)
			}
			for _, v := range []string{fakeCred(t, ownerAccount), fakeCred(t, config.DefaultClaudeAccount)} {
				if strings.Contains(s.audit.String(), v) || strings.Contains(s.logs.String(), v) || strings.Contains(string(body), v) {
					t.Fatal("a credential's value reached the audit line, the log or the refusal body")
				}
			}
			// The operator's own line, which is what a halt like this one is
			// diagnosed from: the class, the sentence and the same list.
			log := s.logs.String()
			for _, want := range []string{
				"type=" + ErrorNoCandidateCredentials,
				"tried=\"" + tc.dropped + " (" + ReasonNotAdmitted + ")\"",
			} {
				if !strings.Contains(log, want) {
					t.Errorf("the refusal line does not carry %q: %s", want, log)
				}
			}
			// One audited act per refusal (C1.13), with the refusal's own type
			// as its status — the class the operator greps for.
			lines := parseAuditLines(t, s.audit.String())
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
			}
			checkAuditShape(t, lines[0])
			if got := lines[0]["status"]; got != ErrorNoCandidateCredentials {
				t.Errorf("the audit line's status is %v, want %q", got, ErrorNoCandidateCredentials)
			}
		})
	}
}

// TestCascadeNoCandidatesOnTheRecordedAuthAtTheListBound: the same refusal at
// the largest list the contract carries. Both halves of it are bounded there —
// the count by maxTriedEntries, the names by the bundle's own bound — and a
// list over either is a body the writer refuses whole and answers as
// state_unavailable, the mis-diagnosis this class exists to end. A credential
// the admission dropped is reported by both walks (the admission's own skips
// and the record's narrowing), and the list names it once.
func TestCascadeNoCandidatesOnTheRecordedAuthAtTheListBound(t *testing.T) {
	names := make([]string, 0, maxTriedEntries)
	var doc strings.Builder
	for i := 0; i < maxTriedEntries; i++ {
		name := fmt.Sprintf("acct-%02d", i)
		names = append(names, name)
		doc.WriteString(config.ClaudeOAuthTokenPrefix + name + ": " + fakeCred(t, name) + "\n")
	}
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.src.docs = [][]byte{[]byte(doc.String())}
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto", ownerAccounts(testRepo)))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount))
		s.state.setRepoAuth(s.slug, creds.RepoAuth{
			Version: creds.RepoAuthVersion, Auth: config.AuthOAuth, Accounts: []string{names[0]},
		})
	})

	resp, body := s.call(t, callBody("oauth-full-list"), declaredCall(config.AuthOAuth, names[0]))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the refusal answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorNoCandidateCredentials {
		t.Fatalf("the refusal is %q, want %q: a list at the contract's own bound must not fall back to it",
			e.Error, ErrorNoCandidateCredentials)
	}
	if len(e.Tried) != maxTriedEntries {
		t.Fatalf("the refused call carries a tried list of %d, want all %d of them", len(e.Tried), maxTriedEntries)
	}
	seen := make(map[string]bool, len(e.Tried))
	for _, tr := range e.Tried {
		if tr.Tier != TierA || tr.Reason != ReasonNotAdmitted {
			t.Errorf("tried %+v is not a tier-A credential the admission dropped", tr)
		}
		if seen[tr.Name] {
			t.Errorf("the tried list names %q twice", tr.Name)
		}
		seen[tr.Name] = true
	}
	if len(seen) != maxTriedEntries {
		t.Errorf("the tried list names %d distinct credentials, want %d", len(seen), maxTriedEntries)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d calls, want none", n)
	}
}
