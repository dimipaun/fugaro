//go:build live

// The live credentials-rules check (docs/gcp-live-checklist.md, check 92):
// what the rules deployed on a real installation do with a run's own identity
// around /creds and around the credentials children the cascade writes
// (credentials-service plan, task C1.4).
//
// It is the owner's, at the owner's terminal, against one installation, and it
// costs nothing: no model call, no image, no bucket. It is what C1.5's section
// runs:
//
//	FUGARO_LIVE_CREDS_RULES_OK=1 \
//	FUGARO_LIVE_INSTALLATION=<the installation, e.g. fugaro> \
//	FUGARO_LIVE_RTDB_URL=https://<fp>-default-rtdb.firebaseio.com \
//	FUGARO_LIVE_FIREBASE_API_KEY=<the restricted web API key> \
//	FUGARO_LIVE_TOKEN_SIGNER=<the token-signer service account email> \
//	  go test -tags live -run TestLiveCredsRules -v ./internal/e2e/
//
// with `gcloud auth application-default login` holding the firebase.database
// and userinfo.email scopes and your user holding fugaroTokenMinter on the
// signer — the same variables, credentials and two calls as check 21's helper
// (FUGARO_LIVE_REQUESTED_BY is optional and is the rb claim of the identities
// it mints). It skips, rather than fails, unless the environment aims it at a
// database, so `go test -tags live ./internal/e2e/` stays usable on a machine
// that is not running the check; an environment that *is* aimed at a database
// without FUGARO_LIVE_CREDS_RULES_OK=1 is refused.
//
// What it writes, and what survives. One scratch repository slug
// (live92-<hex>-rules-<hash>), one throwaway run id, three scratch credential
// names: the registry entry and its two new children, the by-tier counters
// under that slug and run, and five nodes under /creds. Every one of them is
// deleted as the owner — by a cleanup registered before the first write, so a
// failure halfway through still cleans up — and the deletion is read back and
// asserted. No project-wide counter is written at all: the reservation a
// positive by-tier write on the run's *own* ledger would need is a multi-path
// write into the global `counted` counters, so the check refuses to perform it
// and writes the zero the leaf accepts (see crCases). The one positive counter
// write is under the scratch repository's own day node.
//
// It never prints a secret. The custom tokens, the ID tokens, the refresh
// tokens and the web API key are registered with the FACT recorder as they are
// created, every error it prints is scrubbed through the same list, and the
// FACT lines it has collected are checked against those secrets before the
// test ends.
//
// The accepted writes are what makes the refusals mean anything: a database
// that refused everything (a dead URL, a token the rules do not honour) would
// fail this check on its positive controls, not pass it.
//
// Every answer is logged as a FACT: line, to paste into the checklist.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// crOKVar is the owner's explicit opt-in. It is what makes this helper
	// refuse to run: the check writes to a real installation's database, and
	// nothing but the owner at a terminal sets this.
	crOKVar = "FUGARO_LIVE_CREDS_RULES_OK"
	// crInstallVar names the installation the owner believes this database is
	// (the value of /fugaro/project, e.g. "fugaro"). The check reads the
	// database's own answer and requires the two to agree, so neither a stray
	// environment nor a stray URL alone can aim it at an installation.
	crInstallVar = "FUGARO_LIVE_INSTALLATION"
	crRTDBVar    = "FUGARO_LIVE_RTDB_URL"
	crAPIKeyVar  = "FUGARO_LIVE_FIREBASE_API_KEY"
	crSignerVar  = "FUGARO_LIVE_TOKEN_SIGNER"
	crByVar      = "FUGARO_LIVE_REQUESTED_BY"

	crDefaultBy = "live-creds-rules-test"
	// crRepoBase is the readable prefix of the scratch repository, as check
	// 21's live21- is: `fugaro budget show --all` must show no live92-*
	// repository when the check is over.
	crRepoBase = "live92-"
	crSub      = "/rules"
)

// crScopes are the scopes the owner's Application Default Credentials need:
// the same two check 21's helper uses, because this check makes the same two
// calls (the REST API as the owner, signJwt as the signer).
var crScopes = []string{"https://www.googleapis.com/auth/firebase.database", "https://www.googleapis.com/auth/userinfo.email"}

// crSkip is what crTargetFrom returns when nothing in the environment aims
// this check anywhere: the test skips. An environment that does aim it at a
// database and leaves the opt-in out is a refusal instead, never a skip.
var crSkip = errors.New("the live credentials-rules check is not aimed at a database")

// crTarget is the owner's aim: which installation, reached how.
type crTarget struct {
	rtdbURL      string
	apiKey       string
	signer       string
	installation string
	requestedBy  string
}

// crTargetFrom reads and checks the owner's environment. getenv is a
// parameter, not os.Getenv, so the guard is a pure function of the environment
// and can be driven without a terminal.
//
// It refuses: an aim at a database without the opt-in; a missing variable; the
// `belong` installation, which no step of this work touches (D147); a URL that
// is not https, that is a bare address or a loopback name (a local emulator or
// a proxy proves nothing about a deployed document), or that carries a path
// (the check speaks to the database root).
func crTargetFrom(getenv func(string) string) (crTarget, error) {
	t := crTarget{
		rtdbURL:      strings.TrimSpace(getenv(crRTDBVar)),
		apiKey:       strings.TrimSpace(getenv(crAPIKeyVar)),
		signer:       strings.TrimSpace(getenv(crSignerVar)),
		installation: strings.TrimSpace(getenv(crInstallVar)),
		requestedBy:  strings.TrimSpace(getenv(crByVar)),
	}
	optIn := strings.TrimSpace(getenv(crOKVar))
	if t.rtdbURL == "" && t.apiKey == "" && t.signer == "" && t.installation == "" && optIn == "" {
		return crTarget{}, crSkip
	}
	if optIn != "1" {
		return crTarget{}, fmt.Errorf("%s is not 1: this check writes to a real installation's database and is yours to run (docs/gcp-live-checklist.md, check 92); set %s=1 to opt in", crOKVar, crOKVar)
	}
	var missing []string
	for _, v := range []struct{ name, value string }{
		{crInstallVar, t.installation}, {crRTDBVar, t.rtdbURL}, {crAPIKeyVar, t.apiKey}, {crSignerVar, t.signer},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return crTarget{}, fmt.Errorf("%s=1 but %s is unset (the same variables check 21's helper needs; see this file's comment)", crOKVar, strings.Join(missing, ", "))
	}
	if strings.EqualFold(t.installation, "belong") {
		return crTarget{}, fmt.Errorf("%s=%q: this check never touches the 'belong' installation (D147); run it against a sandbox installation", crInstallVar, t.installation)
	}
	if strings.ContainsAny(t.installation, "/~") {
		return crTarget{}, fmt.Errorf("%s=%q is not an installation name", crInstallVar, t.installation)
	}
	u, err := url.Parse(t.rtdbURL)
	switch {
	case err != nil:
		return crTarget{}, fmt.Errorf("%s=%q is not a URL: %v", crRTDBVar, t.rtdbURL, err)
	case u.Scheme != "https":
		return crTarget{}, fmt.Errorf("%s=%q: the database is reached over https", crRTDBVar, t.rtdbURL)
	case u.Hostname() == "":
		return crTarget{}, fmt.Errorf("%s=%q names no host", crRTDBVar, t.rtdbURL)
	case crIsLocalOrIP(u.Hostname()):
		return crTarget{}, fmt.Errorf("%s=%q: a loopback name or a bare address is a local emulator or a proxy, and this check is about the document a real installation deploys", crRTDBVar, t.rtdbURL)
	case u.Path != "" && u.Path != "/":
		return crTarget{}, fmt.Errorf("%s=%q carries a path: the check speaks to the database root", crRTDBVar, t.rtdbURL)
	}
	if t.requestedBy == "" {
		t.requestedBy = crDefaultBy
	}
	return t, nil
}

// crIsLocalOrIP reports whether a host is loopback by name, or any literal IP.
// The real database is a DNS name: an address is a local emulator, a proxy or
// a hand-written URL, and none of the three is the deployed document.
func crIsLocalOrIP(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	return net.ParseIP(h) != nil
}

// crFacts records every FACT: line the check prints, so that the lines the
// owner pastes can be checked against the secrets the run handled before the
// test ends.
type crFacts struct {
	t       *testing.T
	lines   []string
	secrets []string
}

func (f *crFacts) fact(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	f.lines = append(f.lines, line)
	f.t.Log("FACT: " + line)
}

// secret registers a value that must never appear in a FACT line or in an
// error the check prints. token.Config.Register's callback is behind this, so
// every token the exchange mints (the custom token, the ID token, the refresh
// token) and the web API key are registered as they are created.
func (f *crFacts) secret(s string) {
	if s != "" {
		f.secrets = append(f.secrets, s)
	}
}

// errText is an error's text with every registered secret replaced: the token
// package's URL carries the web API key, and a transport error quotes the URL.
func (f *crFacts) errText(err error) string {
	if err == nil {
		return ""
	}
	return crScrub(err.Error(), f.secrets)
}

// checkSecrets reports one error per FACT line that carries a secret. It is a
// method so the check runs on the lines the whole test collected, including
// the ones the cleanup emits after the body is done.
func (f *crFacts) checkSecrets() {
	for _, line := range crSecretsIn(f.lines, f.secrets) {
		f.t.Error(line)
	}
}

// crSecretsIn returns one sentence per FACT line that carries a secret. The
// line is scrubbed before it is quoted, so the report cannot leak what it
// reports.
func crSecretsIn(lines, secrets []string) []string {
	var out []string
	for _, line := range lines {
		for _, s := range secrets {
			if s != "" && strings.Contains(line, s) {
				out = append(out, "a FACT line carries a secret ("+fmt.Sprint(len(s))+" characters): "+crScrub(line, secrets))
			}
		}
	}
	return out
}

// crScrub replaces every registered secret in s.
func crScrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}

// crNames are the scratch names one run of the check works under. Nothing
// here names a real repository, a real run or a real credential: the slug
// carries a hash of a repository that does not exist, and the credential names
// are the check's own.
type crNames struct {
	repo, slug, run                string
	otherRepo, otherSlug, otherRun string
	n1, n2, n3                     string // scratch credential names
	day, oldDay                    int64  // today's epoch day, and one DAYOK refuses
	resumeAt                       int64  // the instant the registry entry's waiting_until holds
}

func crNewNames(now time.Time) crNames {
	h := crHex(3)
	n := crNames{
		repo:      crRepoBase + h + crSub,
		otherRepo: crRepoBase + h + "-other" + crSub,
		day:       budget.Day(now),
		oldDay:    budget.Day(now) - 2, // DAYOK admits today and yesterday only
		resumeAt:  now.Add(15 * time.Minute).UnixMilli(),
	}
	var err error
	if n.slug, err = task.Slug("fake", n.repo); err != nil {
		panic(err)
	}
	if n.otherSlug, err = task.Slug("fake", n.otherRepo); err != nil {
		panic(err)
	}
	stamp := now.UTC().Format("20060102-150405")
	n.run, n.otherRun = stamp+"-"+crHex(2), stamp+"-"+crHex(2)
	n.n1, n.n2, n.n3 = crRepoBase+h+"-a1", crRepoBase+h+"-a2", crRepoBase+h+"-a3"
	return n
}

func crHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// crCredsMark is a mark under /creds in the shape the service writes (a state,
// two instants and a backoff count). /creds is denied to every client, so the
// shape is not validated by anything; it is what the denial is proven against.
func crCredsMark(now time.Time) map[string]any {
	return map[string]any{"state": "exhausted", "until": now.Add(time.Hour).UnixMilli(), "since": now.UnixMilli(), "backoff": 2}
}

// crCase is one write the run's own identity attempts.
type crCase struct {
	name  string
	path  string
	seed  any // what the owner writes there first (nil: nothing)
	value any // what the run writes (nil: a delete)
	allow bool
	why   string // the rule that decides it
}

// crCases is every write the run's own identity attempts, and what the
// deployed rules must answer. The table is data so that crCheckCases can
// assert its safety invariants before the first write, and it mirrors
// internal/budget/rules' emulator cases (creds_emulator_test.go, build tag
// firebase) family for family: that suite is the CI-run proof of the matrix
// against the generated document, and this is the same matrix against the
// document the installation actually deploys.
//
// maxRes is config/limits/maxReserveMicros, read as the run before the table
// is built; the one case that needs it is the write past the bound.
// requestedBy is the rb claim every identity this check mints carries, and so
// the value of the entry's own requestedBy field: RBOK ties each child of the
// entry to it.
//
// The allowed cases come first, path by path, and crCheckCases enforces that:
// an accepted write changes what the next case on the same path sees.
func crCases(n crNames, maxRes int64, requestedBy string) []crCase {
	entry := budget.PathAgent(n.slug, n.run)
	repoDay := budget.PathSpendRepo(n.day, n.slug) + "/byTier"
	runDay := budget.PathSpendRun(n.day, n.slug, n.run) + "/byTier"
	runLedger := budget.PathRun(n.slug, n.run) + "/byTier"
	oldRepoDay := budget.PathSpendRepo(n.oldDay, n.slug) + "/byTier"
	oldRunDay := budget.PathSpendRun(n.oldDay, n.slug, n.run) + "/byTier"
	otherRepoDay := budget.PathSpendRepo(n.day, n.otherSlug) + "/byTier"
	otherRunLedger := budget.PathRun(n.slug, n.otherRun) + "/byTier"
	otherRepoLedger := budget.PathRun(n.otherSlug, n.run) + "/byTier"
	cp := crCredsPaths(n)

	// amount is the one positive counter write the check makes: it is what
	// proves the repository's day leaf accepts a real amount, and it lives
	// under the scratch slug. With no maxReserveMicros bound deployed the leaf
	// accepts only a zero, and the zero is what is written.
	amount := int64(1)
	if maxRes < 1 {
		amount = 0
	}
	return []crCase{
		// The registry entry and the two new children of C1.3 rule 2.
		{name: "create the run's registry entry", path: entry, allow: true,
			value: map[string]any{"repo": n.repo, "auth": "cascade", "startedAt": time.Now().UnixMilli(), "requestedBy": requestedBy},
			why:   "agents/$slug/$run: .write RUN, .validate hasChildren(['repo', 'requestedBy'])"},
		{name: "the name of the credential the run is using", path: entry + "/credential", allow: true, value: n.n1,
			why: "credential: STR && RBOK"},
		{name: "the instant the run expects to resume at", path: entry + "/waiting_until", allow: true, value: n.resumeAt,
			why: "waiting_until: TIMESTAMP && RBOK"},
		// The by-tier split of C1.3 rule 3. The run's own two leaves are
		// written with a zero on purpose: a positive amount needs a
		// reservation, and a reservation is one multi-path write into
		// spend/$day/global/counted and spend/$day/repos/$slug/counted
		// (COUNTERS_FOLLOW) — a rules check must not move the project's
		// counters. Zero is what those two leaves accept with no reservation.
		{name: "the run's own tier-B lifetime counter", path: runLedger + "/b/spent", allow: true, value: int64(0),
			why: "byTier/$tier/spent: $tier is 'b' or 'c', AMOUNT, within the run's reservation (none: zero)"},
		{name: "the run's own tier-B share of the day", path: runDay + "/b/spent", allow: true, value: int64(0),
			why: "the same leaf on the run's day share"},
		{name: "this repository's tier-B day counter", path: repoDay + "/b/spent", allow: true, value: amount,
			why: "spend/$day/repos/$slug/byTier/$tier/spent: TOKEN && fs == $slug && DAYOK, AMOUNT, increase <= maxReserveMicros"},
		{name: "this repository's tier-C day counter", path: repoDay + "/c/spent", allow: true, value: amount,
			why: "the same leaf: the tier guard admits 'c' as it admits 'b'"},

		// The registry entry's children, refused: the credential name is a
		// bounded string tied to the launcher's rb, the waiting time is a
		// timestamp, and the entry is a closed allowlist.
		{name: "a credential name that is a number", path: entry + "/credential", value: 7, why: "credential: STR"},
		{name: "a credential name that is a boolean", path: entry + "/credential", value: true, why: "credential: STR"},
		{name: "a credential name that is an object", path: entry + "/credential", value: map[string]any{"name": n.n1}, why: "credential: STR"},
		{name: "a credential name past the bound", path: entry + "/credential", value: strings.Repeat("x", rules.MaxString+1), why: "credential: STR (at most MaxString characters)"},
		{name: "a field the registry entry does not name", path: entry + "/credential_value", value: n.n1, why: "agents/$slug/$run/$other: .validate false"},
		{name: "a waiting time that is negative", path: entry + "/waiting_until", value: -1, why: "waiting_until: TIMESTAMP (>= 0)"},
		{name: "a waiting time that is fractional", path: entry + "/waiting_until", value: 1.5, why: "waiting_until: TIMESTAMP (whole milliseconds)"},
		{name: "a waiting time that is a string", path: entry + "/waiting_until", value: "2026-10-19T12:00:00Z", why: "waiting_until: TIMESTAMP"},
		{name: "a waiting time past the bound", path: entry + "/waiting_until", value: int64(rules.MaxTimestamp) + 1, why: "waiting_until: TIMESTAMP (at most MaxTimestamp)"},

		// The by-tier split, refused.
		{name: "a tier the split does not have", path: runLedger + "/a/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "an upper-case tier", path: runLedger + "/A/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "more than the run reserved", path: runLedger + "/b/spent", value: int64(1000), why: "the run's leaf: NEW + released <= reserved"},
		{name: "a tier the run's day share does not have", path: runDay + "/a/spent", value: int64(0), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "more than the run reserved, on its day share", path: runDay + "/b/spent", value: int64(1000), why: "the day share's leaf: NEW + released <= reserved"},
		{name: "another repository's lifetime counter", path: otherRepoLedger + "/b/spent", value: int64(0), why: "RUN: fs == $slug"},
		{name: "another run's lifetime counter", path: otherRunLedger + "/b/spent", value: int64(0), why: "RUN: fr == $run"},
		{name: "another repository's day counter", path: otherRepoDay + "/b/spent", value: int64(1), why: "spend/$day/repos/$slug: fs == $slug"},
		{name: "another day's repository counter", path: oldRepoDay + "/b/spent", value: int64(1), why: "DAYOK: $day is today or yesterday"},
		{name: "another day's share of the run", path: oldRunDay + "/b/spent", value: int64(1), why: "DAYOK: $day is today or yesterday"},
		{name: "a tier the repository's day counter does not have", path: repoDay + "/a/spent", value: int64(1), why: "byTier/$tier: $tier == 'b' || $tier == 'c'"},
		{name: "a negative counter", path: repoDay + "/b/spent", value: int64(-1), why: "AMOUNT: >= 0"},
		{name: "a fractional counter", path: repoDay + "/b/spent", value: 1.5, why: "AMOUNT: whole micro-dollars"},
		{name: "a counter that is a string", path: repoDay + "/b/spent", value: "1000", why: "AMOUNT: a number"},
		{name: "a counter that falls", path: repoDay + "/b/spent", seed: int64(5), value: int64(1), why: "NEW >= OLD: a counter is increase-only"},
		{name: "a write past maxReserveMicros", path: repoDay + "/b/spent", seed: int64(5), value: int64(5) + maxRes + 1,
			why: "NEW - OLD <= maxReserveMicros"},

		// /creds, refused: the state the service alone writes as its own
		// account (D136, O1). No run token holds a grant anywhere under it.
		{name: "a mark under /creds", path: cp.mark1, value: crCredsMark(time.Now()), why: "creds: .read false, .write false"},
		{name: "a field of a mark under /creds", path: cp.mark1 + "/state", value: "exhausted", why: "creds: .read false, .write false"},
		{name: "a new mark under /creds", path: cp.mark3, value: crCredsMark(time.Now()), why: "creds: .read false, .write false"},
		{name: "a lease under /creds", path: cp.lease, value: map[string]any{"credential": n.n2}, why: "creds: .read false, .write false"},
		{name: "this run's sticky credential", path: cp.current, value: n.n2, why: "creds: .read false, .write false"},
		{name: "the round-robin counter", path: cp.roundrobin, value: 99, why: "creds: .read false, .write false"},
		{name: "deleting a mark under /creds", path: cp.mark1, value: nil, why: "creds: .write false (a delete is a write)"},
		{name: "deleting the round-robin counter", path: cp.roundrobin, value: nil, why: "creds: .write false (a delete is a write)"},

		// The two generation markers, refused: nothing under config is a
		// client's to write, and a run that could write either could make a
		// run pass a gate the installation has not met.
		{name: "the credentials rules marker", path: rules.PathCredsRulesMarker(), value: rules.CredsRulesVersion, why: "config: no .write for any client; the deploy writes it over IAM"},
		{name: "the policy rules marker", path: projectpolicy.PathRulesMarker(), value: projectpolicy.RulesVersion, why: "config: no .write for any client; the deploy writes it over IAM"},
	}
}

// crCheckCases asserts the table's own safety invariants, before anything is
// written: they are what makes the table safe to run at all, and a table that
// breaks one must fail here rather than on a live installation.
//
//  1. an accepted write names the scratch slug: nothing real is written, and
//     nothing outside the scratch names is written at all;
//  2. nothing under /creds, and nothing under config/, is ever accepted;
//  3. no accepted write names the project-wide day counter;
//  4. a delete is never accepted;
//  5. the first case on a path does not seed it, and an accepted case is the
//     first on its path — an accepted write changes what the next case on the
//     same path sees, and a seeded one would change what the accepted case
//     sees.
func crCheckCases(cases []crCase, scratch ...string) error {
	seen := map[string]bool{}
	for i, c := range cases {
		switch {
		case c.name == "":
			return fmt.Errorf("case %d (/ %s) has no name", i, c.path)
		case c.path == "":
			return fmt.Errorf("case %q has no path", c.name)
		case c.why == "":
			return fmt.Errorf("case %q (/ %s) names no rule", c.name, c.path)
		}
		first := !seen[c.path]
		seen[c.path] = true
		if first && c.seed != nil {
			return fmt.Errorf("case %q seeds /%s before anything has read it as it is: the first case on a path must not seed", c.name, c.path)
		}
		if strings.HasPrefix(c.path, "creds/") && c.allow {
			return fmt.Errorf("case %q accepts a write under /creds: no run token holds a grant there (O1)", c.name)
		}
		if strings.HasPrefix(c.path, "config/") && c.allow {
			return fmt.Errorf("case %q accepts a write under config/: nothing under it is a client's to write", c.name)
		}
		if c.value == nil && c.allow {
			return fmt.Errorf("case %q accepts a delete: a delete is not validated, so nothing may accept one", c.name)
		}
		if !c.allow {
			continue
		}
		if c.seed != nil {
			return fmt.Errorf("case %q is accepted and seeds its own path", c.name)
		}
		if !first {
			return fmt.Errorf("case %q is accepted but another case on /%s ran before it", c.name, c.path)
		}
		if !crNamesAny(c.path, scratch) {
			return fmt.Errorf("case %q is accepted and names no scratch repository: /%s would write something real", c.name, c.path)
		}
		if strings.Contains(c.path, "/global/") {
			return fmt.Errorf("case %q is accepted and names the project-wide day counter: /%s", c.name, c.path)
		}
	}
	return nil
}

func crNamesAny(path string, scratch []string) bool {
	for _, s := range scratch {
		if s != "" && strings.Contains(path, s) {
			return true
		}
	}
	return false
}

// TestLiveCredsRules is check 92's helper: it proves on a live installation
// that the deployed document denies a run's identity every read and every
// write under /creds, accepts its own well-formed writes of the credentials
// children the cascade adds, and refuses everything else those children admit.
func TestLiveCredsRules(t *testing.T) {
	target, err := crTargetFrom(os.Getenv)
	if errors.Is(err, crSkip) {
		t.Skip("check 92 is yours, against one installation: set " + crOKVar + "=1 and the variables this file's comment names (docs/gcp-live-checklist.md)")
	}
	if err != nil {
		t.Fatal(err)
	}
	facts := &crFacts{t: t}
	// Registered first so it runs last: the cleanup's own FACT lines are
	// checked against the secrets too.
	defer facts.checkSecrets()
	ctx := context.Background()
	now := time.Now()
	n := crNewNames(now)

	owner, err := crOwner(ctx, target)
	if err != nil {
		t.Fatal(facts.errText(err))
	}
	// The cleanup is registered before the first write and is idempotent: a
	// failure anywhere below still leaves the installation as it was found.
	clean := &crCleanup{paths: crWritten(n)}
	defer clean.run(t, target, facts)

	facts.fact("the check works under one scratch repository (slug %s, run %s) and three scratch credential names (%s, %s, %s): no real repository's counters and no real credential's mark are touched", n.slug, n.run, n.n1, n.n2, n.n3)

	// 1. The installation, stated twice: what the owner said, and what the
	// database says. The rules compare fp with /fugaro/project, so a
	// disagreement here would make every case below fail for a reason that has
	// nothing to do with what the check is about.
	install, err := crInstall(ctx, facts, owner)
	if err != nil {
		t.Fatal(err)
	}
	if install != target.installation {
		t.Fatalf("the database at %s names the installation %q, but %s names %q: aim the check at the installation the database belongs to (the rules compare every run token's fp claim with %s exactly)", target.rtdbURL, install, crInstallVar, target.installation, budget.PathProject)
	}
	facts.fact("the database at %s names the installation %q, the same one %s names, and it is not belong", target.rtdbURL, install, crInstallVar)

	// 2. The two generation markers, as the owner: the credentials document
	// has to be deployed, or every case below would be about another document.
	credsMarker, err := crMarker(ctx, facts, owner, rules.PathCredsRulesMarker())
	if err != nil {
		t.Fatal(err)
	}
	if credsMarker != rules.CredsRulesVersion {
		t.Fatalf("%s reads %d, want %d: the credentials rules are not deployed on this installation, so nothing below would be about them (run `fugaro init --firebase` from a release that deploys them)", rules.PathCredsRulesMarker(), credsMarker, rules.CredsRulesVersion)
	}
	policyMarker, err := crMarker(ctx, facts, owner, projectpolicy.PathRulesMarker())
	if err != nil {
		t.Fatal(err)
	}
	facts.fact("%s reads %d (this release's rules.CredsRulesVersion) and %s reads %d (projectpolicy.RulesVersion is %d)",
		rules.PathCredsRulesMarker(), credsMarker, projectpolicy.PathRulesMarker(), policyMarker, projectpolicy.RulesVersion)
	if policyMarker != projectpolicy.RulesVersion {
		t.Errorf("%s reads %d, want %d: the deployed document is not the one this release generates, so what the cases below prove is another document's behaviour (check 82 is the deploy's own check)", projectpolicy.PathRulesMarker(), policyMarker, projectpolicy.RulesVersion)
	}

	// 3. The run's identity, minted and exchanged as the launcher does.
	signer, err := crSigner(ctx, target, facts)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := crMint(ctx, facts, signer, target, n.slug, n.run, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := crExchange(ctx, facts, target, custom)
	if err != nil {
		t.Fatal(err)
	}
	run, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: sess.Token})
	if err != nil {
		t.Fatal(facts.errText(err))
	}
	if c := sess.Claims(); c.Slug != budget.Key(n.slug) || c.Run != budget.Key(n.run) || c.FP != install || c.RB != target.requestedBy || c.FX.IsZero() {
		t.Errorf("the identity carries %+v, want fs=%s fr=%s fp=%s rb=%s and an fx", c, budget.Key(n.slug), budget.Key(n.run), install, target.requestedBy)
	}
	facts.fact("the exchange produced the identity %s (expires %s), carrying fs/fr/fx/fp/rb as minted, with no sign-in provider enabled and the restricted web key", sess.UID(), sess.Expiry().UTC().Format(time.RFC3339))

	// 4. The markers as the run: a cascade launch reads them with the run's own
	// identity (C1.19), and neither is a run's to write.
	maxResPath := budget.PathLimits + "/maxReserveMicros"
	maxRes, err := crMaxReserve(ctx, facts, run)
	if err != nil {
		t.Fatalf("%s", facts.errText(err))
	}
	facts.fact("the run's own identity read %s (=%d micro-dollars, the bound every counter leaf carries)", maxResPath, maxRes)
	if runCredsMarker, err := crMarker(ctx, facts, run, rules.PathCredsRulesMarker()); err != nil {
		t.Errorf("a run's identity could not read %s: %s", rules.PathCredsRulesMarker(), facts.errText(err))
	} else {
		facts.fact("the run's own identity read %s (=%d, the gate a cascade launch reads before it starts)", rules.PathCredsRulesMarker(), runCredsMarker)
	}

	// 5. The table, and its own safety invariants, before the first write.
	cases := crCases(n, maxRes, target.requestedBy)
	if err := crCheckCases(cases, budget.Key(n.slug), budget.Key(n.otherSlug)); err != nil {
		t.Fatalf("the case table is not safe to run: %v", err)
	}

	// 6. Seed, as the owner, what the denials are proven against: a read that
	// came back empty because there was nothing to read would not be a denial.
	if err := crSeed(t, ctx, facts, owner, n, now); err != nil {
		t.Fatal(err)
	}
	facts.fact("seeded under /creds as the owner: %s", strings.Join(crCredsPaths(n).seeded(), ", "))

	// 7. Every read a run's identity must not be able to make under /creds.
	reads := crCredsPaths(n).all()
	reads = append(reads, "creds", "creds/marks")
	if denied := crDeniedReads(t, ctx, facts, run, reads); denied != len(reads) {
		t.Errorf("%d of %d reads under /creds were refused; every one must be", denied, len(reads))
	} else {
		facts.fact("a run's identity read nothing under /creds: %d reads refused (permission denied), including the marks, the leases, its own sticky credential and the round-robin counter", denied)
	}

	// 8. Every write: accepted where the run's own identity must be able to
	// write, refused everywhere else, with the node read back as the owner
	// before and after each attempt.
	accepted, refused := crRunCases(t, ctx, facts, owner, run, cases)
	for _, c := range accepted {
		facts.fact("the rules accepted /%s = %s (%s; %s)", c.path, crJSON(c.value), c.name, c.why)
	}
	if len(refused) > 0 {
		facts.fact("the rules refused %d writes a run's identity must not make: %s", len(refused), crCaseNames(refused))
	}

	// 9. The launcher's rb: the entry's children are tied to the launcher that
	// wrote the entry (RBOK), so the same run's identity with another
	// launcher's rb is refused. The accepted writes above are what makes the
	// refusals mean something; this one is what makes them mean *this* run's.
	if err := crOtherLauncher(t, ctx, facts, target, n, owner, now); err != nil {
		t.Error(err)
	}
}

// crOwner is the client the owner's own credentials give: Application Default
// Credentials with the Firebase database and userinfo scopes, over IAM, which
// bypasses the rules exactly as it does for `fugaro init`.
func crOwner(ctx context.Context, target crTarget) (*rtdb.Client, error) {
	ts, err := google.DefaultTokenSource(ctx, crScopes...)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials with the Firebase scopes (gcloud auth application-default login --scopes=https://www.googleapis.com/auth/cloud-platform,%s,%s): %w", crScopes[0], crScopes[1], err)
	}
	return rtdb.New(target.rtdbURL, rtdb.Auth{Source: ts})
}

// crSigner is the token signer, reached with the owner's ADC cloud-platform
// scope: signJwt as the signer, and no role on the signer for the signer
// itself.
func crSigner(ctx context.Context, target crTarget, facts *crFacts) (token.Signer, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials for signJwt: %w", err)
	}
	s, err := token.NewIAMSigner(target.signer, ts)
	if err != nil {
		return nil, errors.New(facts.errText(err))
	}
	return s, nil
}

// crMint mints one run's custom token as the launcher would, and registers it
// as a secret before anything can print it.
func crMint(ctx context.Context, facts *crFacts, signer token.Signer, target crTarget, slug, run string, now time.Time) (string, error) {
	claims := token.Claims{Slug: budget.Key(slug), Run: budget.Key(run), FX: token.ExpiryFX(now, 2*time.Hour), FP: target.installation, RB: target.requestedBy}
	custom, err := token.MintAt(ctx, signer, claims, now)
	if err != nil {
		return "", errors.New(facts.errText(err))
	}
	facts.secret(custom)
	return custom, nil
}

// crExchange trades a custom token for the run's live Firebase identity. Every
// token the exchange creates is registered with the FACT recorder through
// Register, so none of them can reach a FACT line or a printed error.
func crExchange(ctx context.Context, facts *crFacts, target crTarget, custom string) (*token.Session, error) {
	facts.secret(target.apiKey)
	sess, err := token.Exchange(ctx, token.Config{APIKey: target.apiKey, Register: facts.secret}, custom)
	if err != nil {
		return nil, fmt.Errorf("exchanging the custom token: %s", facts.errText(err))
	}
	facts.secret(sess.Token())
	return sess, nil
}

// crInstall reads /fugaro/project: the installation name every run token's fp
// claim must equal.
func crInstall(ctx context.Context, facts *crFacts, owner *rtdb.Client) (string, error) {
	var install string
	found, err := owner.Get(ctx, budget.PathProject, &install)
	if err != nil {
		return "", fmt.Errorf("reading %s as the owner: %s", budget.PathProject, facts.errText(err))
	}
	if !found || install == "" {
		return "", fmt.Errorf("%s is not set: the database was not initialised by `fugaro init --firebase`", budget.PathProject)
	}
	if strings.EqualFold(install, "belong") {
		return "", fmt.Errorf("%s names the 'belong' installation: this check never touches it (D147)", budget.PathProject)
	}
	return install, nil
}

// crMarker reads one generation marker with c's identity.
func crMarker(ctx context.Context, facts *crFacts, c *rtdb.Client, path string) (int, error) {
	var v int
	found, err := c.Get(ctx, path, &v)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %s", path, facts.errText(err))
	}
	if !found {
		return 0, fmt.Errorf("%s is not set: the deploy that writes it has not run, or the rules under it are older than it", path)
	}
	return v, nil
}

// crMaxReserve reads the largest lease the counter leaves are bounded by, with
// the run's own identity: it is under config/limits, which every run token of
// the project reads. It has to be there: every installation `fugaro init
// --firebase` writes it, and the leaves the check writes a positive amount to
// are bounded by it, so its absence would turn those writes into refusals for
// a reason that is not what this check is about.
func crMaxReserve(ctx context.Context, facts *crFacts, run *rtdb.Client) (int64, error) {
	var v int64
	found, err := run.Get(ctx, budget.PathLimits+"/maxReserveMicros", &v)
	if err != nil {
		return 0, fmt.Errorf("reading %s/maxReserveMicros as the run: %s", budget.PathLimits, facts.errText(err))
	}
	if !found {
		return 0, fmt.Errorf("%s/maxReserveMicros is not set: the counter leaves are bounded by it, and an installation without it can lease nothing (an operator runs `fugaro init --firebase` again)", budget.PathLimits)
	}
	return v, nil
}

// crCreds is every location under /creds the check names: the nodes the
// service writes there (C1.9), at the depth it writes them. They are built
// once, with budget.Key like every path in this tree, so the table, the seed,
// the denied reads and the cleanup cannot disagree about a path.
type crCreds struct {
	mark1, mark2, mark3, lease, roundrobin string
	currentParent, current                 string // the sticky credential, and the node it lives under
}

// seeded are the nodes the owner writes, and so the ones a denial is proven
// against.
func (c crCreds) seeded() []string {
	return []string{c.mark1, c.mark2, c.lease, c.current, c.roundrobin}
}

// all are every node under /creds the check names, for the reads.
func (c crCreds) all() []string {
	return append(c.seeded(), c.mark3)
}

// cleanup are the nodes the check deletes. It is not all(): the sticky
// credential itself is named by currentParent instead, because one multi-path
// update may not name a node and its own descendant, and deleting the parent
// deletes the child.
func (c crCreds) cleanup() []string {
	return []string{c.mark1, c.mark2, c.mark3, c.lease, c.currentParent, c.roundrobin}
}

func crCredsPaths(n crNames) crCreds {
	slug, run := budget.Key(n.slug), budget.Key(n.run)
	return crCreds{
		mark1:         "creds/marks/" + budget.Key(n.n1),
		mark2:         "creds/marks/" + budget.Key(n.n2),
		mark3:         "creds/marks/" + budget.Key(n.n3),
		lease:         "creds/leases/" + run,
		currentParent: "creds/current/" + slug,
		current:       "creds/current/" + slug + "/" + run,
		roundrobin:    "creds/roundrobin/" + budget.Key(n.n1),
	}
}

// crWritten is every node the check may write, in one list: the cleanup
// deletes exactly these, and nothing else, whatever happened before it. No
// entry here is an ancestor of another, so they go out in one update.
func crWritten(n crNames) []string {
	return append([]string{
		"agents/" + budget.Key(n.slug),
		budget.PathRun(n.slug, n.run),
		budget.PathSpendRepo(n.day, n.slug),
		budget.PathSpendRun(n.day, n.slug, n.run),
	}, crCredsPaths(n).cleanup()...)
}

// crSeed writes, as the owner, the nodes a denial has to be proven against.
func crSeed(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, n crNames, now time.Time) error {
	t.Helper()
	cp := crCredsPaths(n)
	mark := crCredsMark(now)
	updates := map[string]any{
		cp.mark1:      mark,
		cp.mark2:      mark,
		cp.lease:      map[string]any{"credential": n.n1, "expires": now.Add(time.Minute).UnixMilli()},
		cp.current:    n.n1,
		cp.roundrobin: 3,
	}
	if err := owner.Patch(ctx, "", updates); err != nil {
		return fmt.Errorf("seeding /creds as the owner: %s", facts.errText(err))
	}
	// The seed is read back: a denial is a denial, but a seed that never
	// landed would make the reads below prove nothing.
	for _, p := range cp.seeded() {
		var raw json.RawMessage
		found, err := owner.Get(ctx, p, &raw)
		if err != nil {
			return fmt.Errorf("reading /%s back after seeding it: %s", p, facts.errText(err))
		}
		if !found {
			return fmt.Errorf("/%s is not there after the owner wrote it", p)
		}
	}
	return nil
}

// crDeniedReads attempts each path with the run's identity and returns how
// many were refused with a permission error. A read that succeeded, or that
// failed for any other reason, is reported.
func crDeniedReads(t *testing.T, ctx context.Context, facts *crFacts, run *rtdb.Client, paths []string) int {
	t.Helper()
	denied := 0
	for _, p := range paths {
		var raw json.RawMessage
		_, err := run.Get(ctx, p, &raw)
		switch {
		case err == nil:
			t.Errorf("a run's identity read /%s, which the rules must refuse", p)
		case !errors.Is(err, rtdb.ErrPermission):
			t.Errorf("reading /%s as a run: want a permission error, got %s", p, facts.errText(err))
		default:
			denied++
		}
	}
	return denied
}

// crRunCases attempts every case with the run's own identity, reading the path
// back as the owner before and after each attempt: a refused write must have
// changed nothing, and an accepted one must have left exactly what it wrote.
func crRunCases(t *testing.T, ctx context.Context, facts *crFacts, owner, run *rtdb.Client, cases []crCase) (accepted, refused []crCase) {
	t.Helper()
	for _, c := range cases {
		if c.seed != nil {
			if err := owner.Patch(ctx, "", map[string]any{c.path: c.seed}); err != nil {
				t.Fatalf("seeding /%s as the owner before %q: %s", c.path, c.name, facts.errText(err))
			}
		}
		before, beforeFound := crRead(t, ctx, facts, owner, c.path)
		err := run.Patch(ctx, "", map[string]any{c.path: c.value})
		after, afterFound := crRead(t, ctx, facts, owner, c.path)
		switch {
		case c.allow && err != nil:
			t.Errorf("%s (/ %s, %s): the deployed rules refused a write a run's own identity must be able to make: %s", c.name, c.path, c.why, facts.errText(err))
		case c.allow && (!afterFound || !crSameJSON(after, c.value)):
			t.Errorf("%s (/ %s): the write was accepted but /%s reads %s, want %s", c.name, c.path, c.path, crJSON(after), crJSON(c.value))
		case c.allow:
			accepted = append(accepted, c)
		case err == nil:
			t.Errorf("%s (/ %s, %s): the deployed rules accepted a write a run's identity must not make; /%s now reads %s", c.name, c.path, c.why, c.path, crJSON(after))
			crUndo(t, ctx, facts, owner, c.path, before, beforeFound)
		case !errors.Is(err, rtdb.ErrPermission):
			t.Errorf("%s (/ %s): want a permission error, got %s", c.name, c.path, facts.errText(err))
		case afterFound != beforeFound || !crSameJSON(after, before):
			t.Errorf("%s (/ %s): refused, but the node changed from %s to %s", c.name, c.path, crJSON(before), crJSON(after))
		default:
			refused = append(refused, c)
		}
	}
	return accepted, refused
}

// crOtherLauncher is the RBOK control: the same run's identity, minted with
// another launcher's rb, is refused the registry entry's credential child.
// Without it the refusals above would be consistent with a database that
// refuses everything, and the accepted writes with a database that admits
// everything.
func crOtherLauncher(t *testing.T, ctx context.Context, facts *crFacts, target crTarget, n crNames, owner *rtdb.Client, now time.Time) error {
	t.Helper()
	other := target
	other.requestedBy = target.requestedBy + "-other-launcher"
	signer, err := crSigner(ctx, target, facts)
	if err != nil {
		return err
	}
	custom, err := crMint(ctx, facts, signer, other, n.slug, n.run, now)
	if err != nil {
		return err
	}
	sess, err := crExchange(ctx, facts, other, custom)
	if err != nil {
		return err
	}
	run, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: sess.Token})
	if err != nil {
		return errors.New(facts.errText(err))
	}
	entry := budget.PathAgent(n.slug, n.run)
	before, beforeFound := crRead(t, ctx, facts, owner, entry+"/credential")
	err = run.Patch(ctx, "", map[string]any{entry + "/credential": n.n2})
	after, afterFound := crRead(t, ctx, facts, owner, entry+"/credential")
	switch {
	case err == nil:
		return fmt.Errorf("the same run's identity with another launcher's rb wrote /%s/credential: the rules accepted a write RBOK must refuse; it now reads %s", entry, crJSON(after))
	case !errors.Is(err, rtdb.ErrPermission):
		return fmt.Errorf("the other launcher's write: want a permission error, got %s", facts.errText(err))
	case afterFound != beforeFound || !crSameJSON(after, before):
		return fmt.Errorf("the other launcher's write was refused but /%s/credential changed from %s to %s", entry, crJSON(before), crJSON(after))
	}
	facts.fact("the same run's identity (uid %s) with another launcher's rb was refused /%s/credential: the entry's children are tied to the launcher that wrote the entry", sess.UID(), entry)
	return nil
}

// crUndo puts a path back the way the owner found it. It is for the one way
// this check could write something outside its scratch names: a case it
// expects the rules to refuse and a live installation accepts — a rules hole,
// or a document older than this release. The nodes the check means to write
// are deleted by crCleanup; these are the nodes it must never write at all,
// among them another repository's day counter and the two generation markers,
// which no cleanup may delete.
func crUndo(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, path string, before any, beforeFound bool) {
	t.Helper()
	value := before
	if !beforeFound {
		value = nil // the node was not there: the write created it
	}
	if err := owner.Patch(ctx, "", map[string]any{path: value}); err != nil {
		t.Errorf("putting /%s back as the owner after the rules accepted a write they must refuse: %s", path, facts.errText(err))
	}
}

// crRead reads a node as the owner, which bypasses the rules: it is how a
// refusal is shown to have changed nothing and an acceptance to have landed.
func crRead(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, path string) (any, bool) {
	t.Helper()
	var raw json.RawMessage
	found, err := owner.Get(ctx, path, &raw)
	if err != nil {
		t.Fatalf("reading /%s as the owner: %s", path, facts.errText(err))
	}
	if !found {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("reading /%s: %s is not JSON: %s", path, raw, facts.errText(err))
	}
	return v, true
}

// crSameJSON compares a value read back from the database with the value that
// was written, as JSON: the database may reorder keys and stores every number
// the same way.
func crSameJSON(got, want any) bool {
	b, err := json.Marshal(want)
	if err != nil {
		return false
	}
	var w any
	if json.Unmarshal(b, &w) != nil {
		return false
	}
	return reflect.DeepEqual(got, w)
}

// crJSON is a value as JSON text, for a FACT line or a failure message.
func crJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// crCaseNames is the case names, comma separated, for one FACT line: which
// writes were refused, without one line each.
func crCaseNames(cases []crCase) string {
	out := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, c.name)
	}
	return strings.Join(out, "; ")
}

// crCleanup deletes every node the check wrote, as the owner, and reads each
// one back to prove it is gone. It runs on the way out of the test whether the
// test passed or failed, and it is idempotent: the nodes it deletes are the
// ones crWritten names, whether or not the run got as far as writing them.
type crCleanup struct {
	paths []string
	done  bool
}

func (c *crCleanup) run(t *testing.T, target crTarget, facts *crFacts) {
	if c.done {
		return
	}
	c.done = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	owner, err := crOwner(ctx, target)
	if err != nil {
		t.Errorf("the check's nodes are still on the installation: %s", facts.errText(err))
		return
	}
	updates := make(map[string]any, len(c.paths))
	for _, p := range c.paths {
		updates[p] = nil
	}
	if err := owner.Patch(ctx, "", updates); err != nil {
		t.Errorf("deleting the check's nodes as the owner: %s", facts.errText(err))
		return
	}
	var survivors []string
	for _, p := range c.paths {
		var raw json.RawMessage
		found, err := owner.Get(ctx, p, &raw)
		if err != nil {
			t.Errorf("reading /%s back after the cleanup: %s", p, facts.errText(err))
			continue
		}
		if found {
			survivors = append(survivors, p)
		}
	}
	if len(survivors) > 0 {
		t.Errorf("these nodes survived the cleanup and are still on the installation: %s", strings.Join(survivors, ", "))
	}
	facts.fact("the check's %d nodes were deleted as the owner and read back absent: %s", len(c.paths), strings.Join(c.paths, ", "))
}
